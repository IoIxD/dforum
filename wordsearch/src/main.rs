use std::net::{TcpListener, TcpStream};
use std::fs::{read_to_string};
use std::io::{Write, BufWriter, BufReader, BufRead};
use regex::Regex;
use lazy_static::lazy_static;

lazy_static! {
    static ref NOT_ALPHABET: Regex = Regex::new("[^A-z]").unwrap();
}

#[tokio::main]
async fn main() -> std::io::Result<()> {
    // the reason it's a server instead of a standalone is so that
    // it can keep the dictionary in memory instead of re-reading it,
    // improving performance.

    // start by opening the dictionary file.
    let words_raw = read_to_string("./src/dictionary.txt")?;
    // despite knowing the size of the file at compile time, we cannot use an array
    // because rust throws a stack overflow trying to make an array as big as the file.
    let words = words_raw
                    .split("\n")
                    .into_iter()
                    .map(|a| {
                        a.to_string()
                    })
                    .collect::<Vec<String>>();
    
    let listener = TcpListener::bind("127.0.0.1:7074")?;
    
    loop {
        match listener.accept() {
            Ok((socket, _addr)) =>  {
                handle_client(socket, &words[0..words.len()]);
            },
            Err(e) => println!("couldn't get client: {e:?}"),
        }
    }
}

fn handle_client(s: TcpStream, banned_words: &[String]) {
    let s_clone = s.try_clone().unwrap();
    let mut reader = BufReader::new(s);
    let mut writer = BufWriter::new(s_clone);

    let mut string = String::new();
    match reader.read_line(&mut string) {
        Ok(..) => {
            let our_words = string
            .split(" ")
            .into_iter()
            .collect::<Vec<_>>();

            let mut allowed_words: Vec<String> = vec!();

            for word in our_words {
                // word formatted as the dictionary.txt likes
                let search = word.to_uppercase().replace("\r","").replace("\n","");
                if allowed_words.contains(&search) {continue}

                // if it has any non-alphabet characters after that, it won't be in the dictionary.txt
                // either way, so skip.
                match NOT_ALPHABET.find(search.as_str()) {
                    Some(..) => continue,
                    None => {}
                }
                
                if search.len() <= 1 {continue;}

                if !banned_words.contains(&search) {
                    allowed_words.push(search);
                }
            }

            writer.write(format!("{:?}\n",allowed_words).as_bytes()).unwrap();
            writer.flush().unwrap();
        }
        Err(err) => {
            writer.write(format!("{:?}\n",err).as_bytes()).unwrap();
            writer.flush().unwrap();
            return;
        }
    };
}