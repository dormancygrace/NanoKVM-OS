const https=require('https'), http2=require('http2');
const p=process.argv[2];let session,req;let bytes=0,frames=0,last=0;const start=performance.now();
function data(buf){if(last===255&&buf[0]===216)frames++;for(let i=0;i<buf.length-1;i++)if(buf[i]===255&&buf[i+1]===216)frames++;last=buf.at(-1);bytes+=buf.length}
function finish(){console.log(JSON.stringify({protocol:p,alpn:session?.alpnProtocol||req.socket.alpnProtocol,bytes,frames,duration_s:(performance.now()-start)/1000}));if(session){req.close();session.close()}else req.destroy()}
const headers={cookie:'nano-kvm-token='+process.env.NANOKVM_TOKEN};
if(p==='h2'){session=http2.connect('https://192.168.4.128',{rejectUnauthorized:false});req=session.request({':path':'/api/stream/mjpeg',...headers});req.on('response',h=>{if(h[':status']!==200)throw Error('status '+h[':status'])});req.on('data',data);req.end()}
else req=https.get('https://192.168.4.128/api/stream/mjpeg',{rejectUnauthorized:false,ALPNProtocols:['http/1.1'],headers},r=>{if(r.statusCode!==200)throw Error('status '+r.statusCode);r.on('data',data)});
req.on('error',e=>{console.error(e);process.exitCode=1});setTimeout(finish,15000);
