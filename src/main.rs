extern crate tokio;

// pages
pub mod server;
pub mod index;
pub mod discord;

use std::convert::Infallible;
use std::fs;
use std::net::SocketAddr;
use hyper::{Body, Request, Response, Server};
use hyper::service::{make_service_fn, service_fn};
use regex::Regex;
use lazy_static::lazy_static;
use toml::Value;

use discord::{Bot};

lazy_static!(
    static ref OTHER_THEN_NUMBERS: Regex = Regex::new(r"[^0-9]").unwrap();
    static ref CONFIG: Value = fs::read_to_string("./config.toml").unwrap().parse::<Value>().unwrap();
    static ref BOT: Bot = Bot::new(CONFIG.get("BotToken").unwrap().as_str().unwrap().to_string()).unwrap();
);

async fn handle(req: Request<Body>) -> Result<Response<Body>, Infallible> {
    // get the path and split it into parts.
    let url = req.uri().to_string();
    let url_parts: Vec<&str> = url.split("/").collect();
    let body: String;
    let mut length = url_parts.len();
    // if the last entry is blank, the length is lower
    if url_parts[length-1] == "" {
        length -= 1;
    }

    // page handling; first, check how many parts to the path there are
    match length {
        // none? (if its blank there's 1 empty space in it)
        1 => {
            // index.
            body = String::from("index");
        },
        // 1?
        2 => {
            // is the part only numbers?
            if OTHER_THEN_NUMBERS.is_match(url_parts[1]) {
                // its another, regular page
                body = format!("page: {}",url_parts[1]);
            } else {
                // its a guild page
                match url_parts[1].parse::<u64>() {
                    Ok(a) => {
                        match BOT.guild_page(a).await {
                            Ok(b) => body = format!("{}",b),
                            Err(err) => body = format!("{}",err),
                        };
                    },
                    Err(err) => body = format!("{}",err),
                };
            }
        },
        3 => {
            // its a channel page
            match url_parts[2].parse::<u64>() {
                Ok(a) => {
                    match BOT.channel_page(a).await {
                        Ok(b) => body = format!("{}",b),
                        Err(err) => body = format!("{}",err),
                    };
                },
                Err(err) => body = format!("{}",err),
            };
        }
        4 => {
            match url_parts[3].parse::<u64>() {
                Ok(a) => {
                    match BOT.post_page(a).await {
                        Ok(b) => body = format!("{}",b),
                        Err(err) => body = format!("{}",err),
                    };
                },
                Err(err) => body = format!("{}",err),
            };
        }
        _ => {
            body = String::from("404")
        }
    };
    Ok(Response::new(Body::from(body)))
}

#[tokio::main]
async fn main() {
    // On one thread, start running the bot.
    tokio::spawn(async {
        loop {
            if let Err(e) = BOT.start().await {
                eprintln!("discord error: {}",e)
            }
        }
    });

    // Construct our SocketAddr to listen on...
    let addr = SocketAddr::from(([127, 0, 0, 1], 8084));

    // And a MakeService to handle each connection...
    let make_service = make_service_fn(|_conn| async {
        Ok::<_, Infallible>(service_fn(handle))
    });

    // Then bind and serve...
    let server = Server::bind(&addr).serve(make_service);

    // And run forever...
    if let Err(e) = server.await {
        eprintln!("server error: {}", e);
    }
}