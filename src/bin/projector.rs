use anyhow::Result;
use clap::Parser;
use projector::{
    config::{Config, Operation},
    opts::Opts,
    projector::Projector,
};

fn main() -> Result<()> {
    let config: Config = Opts::parse().try_into()?;
    let mut proj = Projector::from_config(config.config, config.pwd);

    match config.operation {
        Operation::Print(None) => {
            let value = proj.get_value_all();
            let value = serde_json::to_string(&value)?;
            println!("{}", value);
        }
        Operation::Print(Some(k)) => {
            if let Some(x) = proj.get_value(&k) {
                println!("{}", x)
            };
        }
        Operation::Add(k, v) => {
            proj.set_value(k, v);
            proj.save()?;
        }
        Operation::Remove(k) => {
            proj.remove_value(&k);
            proj.save()?;
        }
    }

    Ok(())
}
