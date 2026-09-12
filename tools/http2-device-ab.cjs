const https = require('https');
const http2 = require('http2');
const fs = require('fs');
const base='https://192.168.4.128';
const headers={cookie:'nano-kvm-token='+process.env.NANOKVM_TOKEN};
async function trial(protocol) {
 const agent = protocol==='h1' ? new https.Agent({keepAlive:true,maxSockets:6,rejectUnauthorized:false}) : null;
 const session = protocol==='h2' ? http2.connect(base,{rejectUnauthorized:false}) : null;
 if(session) await new Promise((resolve,reject)=>{session.once('connect',resolve);session.once('error',reject)});
 const lat=[]; let bytes=0;
 async function request(){
  const start=performance.now();
  await new Promise((resolve,reject)=>{
   const r=session?session.request({':path':'/api/vm/cpu-frequency',...headers}):https.get(base+'/api/vm/cpu-frequency',{agent,headers},handle);
   function handle(res){if(res.statusCode!==200)return reject(new Error('HTTP '+res.statusCode));res.on('data',d=>bytes+=d.length);res.on('end',resolve);res.on('error',reject)}
   if(session){r.on('response',h=>{if(h[':status']!==200)reject(new Error('HTTP '+h[':status']))});r.on('data',d=>bytes+=d.length);r.on('end',resolve);r.end()}
   r.on('error',reject);
  });lat.push(performance.now()-start);
 }
 for(let i=0;i<6;i++) await request();lat.length=0;bytes=0;
 const start=performance.now();
 await Promise.all(Array.from({length:6},async()=>{for(let i=0;i<30;i++)await request()}));
 const elapsed=performance.now()-start;lat.sort((a,b)=>a-b);
 const result={protocol,alpn:session?.alpnProtocol??'http/1.1',requests:lat.length,elapsed_ms:elapsed,p50_ms:lat[Math.floor(lat.length*.5)],p95_ms:lat[Math.floor(lat.length*.95)],bytes};
 if(session)session.close();if(agent)agent.destroy();return result;
}
(async()=>{const out=[];for(const p of ['h1','h2','h2','h1']){out.push(await trial(p));await new Promise(r=>setTimeout(r,1000))}console.log(JSON.stringify(out,null,2));})().catch(e=>{console.error(e);process.exitCode=1});
