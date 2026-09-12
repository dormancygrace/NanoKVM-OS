const https = require('https');
const crypto = require('crypto');
const fs = require('fs');
const duration = Number(process.env.MJPEG_SECONDS || 15) * 1000;
const prefix = process.env.MJPEG_OUTPUT;
if (!process.env.NANOKVM_TOKEN || !prefix) throw new Error('Missing session/output');
const start = Date.now();
const results=[]; const handles=[];
function client(name, delay=0, slow=false) {
 const result={name,frames:0,bytes:0,duplicateFrames:0,imageDuplicateFrames:0,requestedAtMs:null,firstFrameMs:null,firstFrameDelayMs:null,errors:[],maxGapMs:0};results.push(result);
 const timer=setTimeout(()=>{
  let buffer=Buffer.alloc(0), previousHash, previousImageHash, previousTime, saved=false;
  result.requestedAtMs=Date.now()-start;
  if(process.env.MJPEG_TRACE==='1')result.frameEvents=[];
  const req=https.get(`https://${process.env.NANOKVM_HOST || '192.168.4.128'}/api/stream/mjpeg`,{rejectUnauthorized:false,headers:{Cookie:`nano-kvm-token=${process.env.NANOKVM_TOKEN}`}},res=>{
   result.httpStatus=res.statusCode;
   if(res.statusCode!==200){result.errors.push(`HTTP ${res.statusCode}`);res.resume();return;}
   res.on('data',chunk=>{
    result.bytes+=chunk.length;buffer=Buffer.concat([buffer,chunk]);
    if(buffer.length>16*1024*1024){result.errors.push('Oversized frame buffer');req.destroy();return;}
    for(;;){
     const end=buffer.indexOf('\r\n\r\n');if(end<0)break;
     const header=buffer.subarray(0,end).toString('ascii');const match=/Content-Length:\s*(\d+)/i.exec(header);
     if(!match){result.errors.push('Missing multipart length');req.destroy();return;}
     const size=Number(match[1]);if(buffer.length<end+4+size)break;
     const jpeg=buffer.subarray(end+4,end+4+size);buffer=buffer.subarray(end+4+size);
     const now=Date.now();if(previousTime)result.maxGapMs=Math.max(result.maxGapMs,now-previousTime);previousTime=now;
     result.frames++;if(result.firstFrameMs===null){result.firstFrameMs=now-start;result.firstFrameDelayMs=result.firstFrameMs-result.requestedAtMs;}
     const hash=crypto.createHash('sha256').update(jpeg).digest('hex');if(hash===previousHash)result.duplicateFrames++;previousHash=hash;
     const nativeHeader=Buffer.from([0xff,0xd8,0xff,0xe9,0,4]);
     const pixels=jpeg.length>8 && jpeg.subarray(0,6).equals(nativeHeader)?jpeg.subarray(8):jpeg;
     const imageHash=crypto.createHash('sha256').update(pixels).digest('hex');
     const changed=imageHash!==previousImageHash;
     if(!changed)result.imageDuplicateFrames++;
     if(result.frameEvents && result.frameEvents.length<3000)result.frameEvents.push({atMs:now-start,changed});
     previousImageHash=imageHash;
     if(!saved&&!slow){fs.writeFileSync(`${prefix}-${name}.jpg`,jpeg);saved=true;}
    }
    if(slow){res.pause();setTimeout(()=>res.resume(),150);}
   });
   res.on('error',e=>{if(Date.now()-start<duration-100)result.errors.push(e.message);});
  });
  req.on('error',e=>{if(Date.now()-start<duration-100)result.errors.push(e.message);});handles.push(req);
 },delay); handles.push({destroy:()=>clearTimeout(timer)});
}
client('fast');
if(process.env.MJPEG_MULTI==='1'){client('slow',1000,true);client('late',5000);}
setTimeout(()=>{
 handles.forEach(h=>h.destroy());
 const summary={startedAt:new Date(start).toISOString(),elapsedMs:Date.now()-start,clients:results};fs.writeFileSync(`${prefix}.json`,JSON.stringify(summary,null,2));console.log(JSON.stringify(summary));
 process.exit(results[0].frames?0:2);
},duration);
