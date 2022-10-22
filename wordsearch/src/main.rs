use std::net::{TcpListener, TcpStream};
use std::fs::{read_to_string};
use std::io::{BufWriter, BufReader, BufRead};
use std::io::Write as wr;
use regex::Regex;
use lazy_static::lazy_static;
use std::collections::HashMap;

lazy_static! {
    static ref NOT_ALPHABET: Regex = Regex::new("[^A-Za-z0-9]").unwrap();
}

#[derive(Debug,Clone)]
struct Tree {
    branches: HashMap<char, Vec<String>>
}

impl Tree {
    fn new(words: Vec<String>) -> Tree {
        let branch = HashMap::new();
        let mut tree = Tree{branches: branch};
        for word in words {
            let nub = match word.chars().nth(0) {
                Some(a) => a,
                None => continue,
            };
            match tree.branches.get_mut(&nub) {
                Some(a) => {
                    a.push(word);
                }
                None => {
                    tree.branches.insert(nub, vec![]);
                    tree.branches.get_mut(&nub).unwrap().push(word);
                }
            }
        }
        tree
    }
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

    // make a new search tree
    let tree = Tree::new(words);
    
    let listener = TcpListener::bind("127.0.0.1:7074")?;
    
    loop {
        match listener.accept() {
            Ok((socket, _addr)) =>  {
                let tree_clone = tree.clone();
                tokio::spawn(async move {
                    handle_client(socket, &tree_clone);
                });
            },
            Err(e) => println!("couldn't get client: {e:?}"),
        }
    }
}



fn handle_client(s: TcpStream, tree: &Tree) {
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
                // special case: the dictionary.txt does not contain apostorphes.
                if word.contains("'") || word.contains("’") {
                    continue
                }

                let search = NOT_ALPHABET.replace_all(word.to_uppercase().as_str(), "").to_string();

                if allowed_words.contains(&search) {continue}

                // if it has any non-alphabet characters after that, it won't be in the dictionary.txt
                // either way, so skip.
                match NOT_ALPHABET.find(search.as_str()) {
                    Some(..) => continue,
                    None => {}
                }
                
                // we don't want to singular letters and two letter words are unlikely to be proper nouns anyways
                if search.len() <= 2 {continue;}

                let char = match search.chars().nth(0) {
                    Some(a) => a,
                    None => continue
                };

                let branch = match tree.branches.get(&char){
                    Some(a) => a,
                    None => {
                        println!("unknown char: {}",&char);
                        continue;
                    }
                };
                
                if !branch.contains(&search) {
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