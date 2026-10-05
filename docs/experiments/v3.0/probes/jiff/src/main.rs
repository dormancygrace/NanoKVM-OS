use serde::{Deserialize,Serialize};
#[derive(Deserialize)]struct Case{name:String,path:String,second:i64}
#[derive(Serialize)]struct ResultCase{name:String,second:i64,offset:i32,error:bool}
fn main(){let cases:Vec<Case>=serde_json::from_reader(std::io::stdin()).unwrap();let out=cases.into_iter().map(|case|{let value=std::fs::read(&case.path).ok().and_then(|data|jiff::tz::TimeZone::tzif(&case.name,&data).ok()).and_then(|tz|jiff::Timestamp::from_second(case.second).ok().map(|time|tz.to_offset(time).seconds()));ResultCase{name:case.name,second:case.second,offset:value.unwrap_or(0),error:value.is_none()}}).collect::<Vec<_>>();serde_json::to_writer(std::io::stdout(),&out).unwrap();}
