use futures::stream::StreamExt;
use std::{env, error::Error, sync::Arc};
use twilight_cache_inmemory::{InMemoryCache, ResourceType};
use twilight_gateway::{cluster::{Cluster, ShardScheme}, Event, Intents};
use twilight_http::Client as HttpClient;
use rust_embed::{RustEmbed, EmbeddedFile};
use eyre::eyre;

// templates
#[derive(RustEmbed)]
#[folder = "resources/templates/"]
struct Templates;

pub struct Bot {
    client: Arc<HttpClient>,
    cache: InMemoryCache,
    pub token: String,
    guild_page: String,
    channel_page: String,
    post_page: String,
}

impl Bot {
    pub fn new(token: String) -> Result<Bot, Box<dyn Error + Send + Sync>> {
        let client = Arc::new(HttpClient::new((&token).clone()));
        let cache = InMemoryCache::builder()
            .resource_types(ResourceType::MESSAGE)
            .build();

        let guildtmpl = match Templates::get("guild.gohtml") {
            Some(a) => a,
            None => panic!("no guild template found")
        };
        let forumtmpl = match Templates::get("forum.gohtml") {
            Some(a) => a,
            None => panic!("no forum template found")
        };
        let posttmpl = match Templates::get("post.gohtml") {
            Some(a) => a,
            None => panic!("no post template found")
        };
        Ok(Bot{
            client: Arc::clone(&client),
            cache: cache,
            token: token,
            guild_page: String::from_utf8(guildtmpl.data.as_ref().to_vec())?,
            channel_page: String::from_utf8(forumtmpl.data.as_ref().to_vec())?,
            post_page: String::from_utf8(posttmpl.data.as_ref().to_vec())?,
        })
    }

    pub async fn start(&self) -> Result<(), Box<dyn Error + Send + Sync>> {
        // Start a single shard.
        let scheme = ShardScheme::Range {
            from: 0,
            to: 0,
            total: 1,
        };
    
        // Specify intents requesting events about things like new and updated
        // messages in a guild and direct messages.
        let intents = Intents::GUILD_MESSAGES | Intents::GUILDS;
    
        let (cluster, mut events) = Cluster::builder(self.token.clone(), intents)
            .shard_scheme(scheme)
            .build()
            .await?;
    
        let cluster = Arc::new(cluster);
    
        // Start up the cluster
        let cluster_spawn = cluster.clone();

        tokio::spawn(async move {
            cluster_spawn.up().await;
        });

        let me = (&self.client).current_user().await?.model().await?;
        println!("Running dforum as {}#{}",me.name, me.discriminator);

        // Since we only care about messages, make the cache only process messages.
        let cache = InMemoryCache::builder()
            .resource_types(ResourceType::MESSAGE)
            .build();

        // Startup an event loop to process each event in the event stream as they
        // come in.
        while let Some((shard_id, event)) = events.next().await {
            // Update the cache.
            cache.update(&event);

            // Spawn a new task to handle the event
            tokio::spawn(handle_event(shard_id, event, Arc::clone(&self.client)));
        }

        Ok(())
    }

    pub async fn guild_page(&self, id: u64) -> Result<&str, Box<dyn Error + Send + Sync>> {
        Ok(&self.guild_page)
    }

    pub async fn channel_page(&self, id: u64) -> Result<&str, Box<dyn Error + Send + Sync>> {
        Ok(&self.channel_page)
    }

    pub async fn post_page(&self, id: u64) -> Result<&str, Box<dyn Error + Send + Sync>> {
        Ok(&self.post_page)
    }
}

async fn handle_event(
    shard_id: u64,
    event: Event,
    http: Arc<HttpClient>,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    match event {
        Event::ShardConnected(_) => {
            println!("Connected on shard {}", shard_id);
        }
        _ => {}
    }

    Ok(())
}