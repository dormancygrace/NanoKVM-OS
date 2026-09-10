import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vite';
import { server as WebSocketServer } from 'websocket';
import base from '../../vite.config.ts';

const events: unknown[]=[];
let delayed=false;
export default defineConfig({
  ...base,
  root:fileURLToPath(new URL('.',import.meta.url)),
  define:{'import.meta.env.VITE_SERVER_IP':JSON.stringify('127.0.0.1'),'import.meta.env.VITE_SERVER_PORT':JSON.stringify('18435')},
  server:{host:'127.0.0.1',port:18435,strictPort:true},
  plugins:[...base.plugins!,{
    name:'terminal-fixture',
    configureServer(server){
      server.middlewares.use((req,res,next)=>{
        if(req.url==='/__fixture/delay'){ delayed=true; res.end('ok'); return; }
        if(req.url==='/__fixture/log'){res.setHeader('Content-Type','application/json');res.end(JSON.stringify(events));return;}
        next();
      });
      const wss=new WebSocketServer({httpServer:server.httpServer!,autoAcceptConnections:false});
      wss.on('request',(req)=>{
        if(req.resourceURL.pathname!=='/api/vm/terminal') return;
        events.push({event:'request'});
        let cancelled=false;
        req.on('requestCancelled',()=>{cancelled=true;events.push({event:'cancelled'});});
        const accept=()=>{
          if(cancelled)return;
          try{
            const ws=req.accept(undefined,req.origin);
            events.push({event:'open'});
            ws.sendBytes(Buffer.from('\x1b[31mRED\x1b[0m \x1b[32mGREEN\x1b[0m Привет\r\n'));
            ws.on('message',(msg)=>events.push(msg.type==='utf8'?{text:msg.utf8Data}:{resize:msg.binaryData.toString()}));
            ws.on('close',()=>events.push({event:'close'}));
          }catch{events.push({event:'accept-closed'});}
        };
        if(delayed){delayed=false;setTimeout(accept,700);}else accept();
      });
      server.httpServer!.on('close',()=>wss.shutDown());
    }
  }]
});
