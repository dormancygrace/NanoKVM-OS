package main
import("crypto/tls";"encoding/binary";"encoding/json";"flag";"fmt";"net/http";"os";"sync";"time";"github.com/gorilla/websocket";"github.com/pion/webrtc/v4")
type message struct{Event string `json:"event"`;Data json.RawMessage `json:"data"`}
type result struct{Client int `json:"client"`;Packets int `json:"packets"`;Bytes int `json:"bytes"`;Gaps int `json:"gaps"`;TimestampErrors int `json:"timestampErrors"`;Error string `json:"error,omitempty"`}
func main(){
 host:=flag.String("host","192.168.4.128","NanoKVM address");seconds:=flag.Int("seconds",15,"capture seconds");clients:=flag.Int("clients",1,"audio listeners");output:=flag.String("output","audio-packets.bin","length-prefixed Opus output for client 0");flag.Parse()
 var wg sync.WaitGroup;rs:=make([]result,*clients)
 for i:=range rs{wg.Add(1);go func(i int){defer wg.Done();rs[i]=run(i,*host,*seconds,*output)}(i)}
 wg.Wait();data,_:=json.MarshalIndent(rs,"","  ");fmt.Println(string(data));for _,r:=range rs{if r.Error!=""||r.Packets==0{os.Exit(1)}}
}
func run(index int,host string,seconds int,output string)(r result){
 r.Client=index;var stats sync.Mutex
 dial:=websocket.Dialer{TLSClientConfig:&tls.Config{InsecureSkipVerify:true}}
 ws,_,err:=dial.Dial("wss://"+host+"/api/stream/audio",http.Header{"Cookie":[]string{"nano-kvm-token="+os.Getenv("NANOKVM_TOKEN")}});if err!=nil{r.Error=err.Error();return};defer ws.Close()
 var first message;if err=ws.ReadJSON(&first);err!=nil{r.Error=err.Error();return}
 var servers []webrtc.ICEServer;if first.Event!="ice-servers"||json.Unmarshal(first.Data,&servers)!=nil{r.Error="no ICE config";return}
 pc,err:=webrtc.NewPeerConnection(webrtc.Configuration{ICEServers:servers});if err!=nil{r.Error=err.Error();return};defer pc.Close()
 var write sync.Mutex;send:=func(event string,data any){write.Lock();defer write.Unlock();_=ws.WriteJSON(map[string]any{"event":event,"data":data})}
 var out *os.File;if index==0{out,err=os.Create(output);if err!=nil{r.Error=err.Error();return};defer out.Close()}
 var trackWG sync.WaitGroup
 pc.OnTrack(func(track *webrtc.TrackRemote,_ *webrtc.RTPReceiver){
  trackWG.Add(1);defer trackWG.Done();var prev uint16;var ts uint32;have:=false
  for{pkt,_,err:=track.ReadRTP();if err!=nil{return};stats.Lock();r.Packets++;r.Bytes+=len(pkt.Payload);if have{if pkt.SequenceNumber!=prev+1{r.Gaps++};if pkt.Timestamp-ts!=960{r.TimestampErrors++}};prev=pkt.SequenceNumber;ts=pkt.Timestamp;have=true
   if out!=nil{var header [2]byte;binary.BigEndian.PutUint16(header[:],uint16(len(pkt.Payload)));_,_=out.Write(header[:]);_,_=out.Write(pkt.Payload)};stats.Unlock()
  }
 })
 _,err=pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio,webrtc.RTPTransceiverInit{Direction:webrtc.RTPTransceiverDirectionRecvonly});if err!=nil{r.Error=err.Error();return}
 offer,err:=pc.CreateOffer(nil);if err!=nil{r.Error=err.Error();return}
 // Send a complete local SDP, avoiding a candidate-before-offer race in the fixture.
 gather:=webrtc.GatheringCompletePromise(pc);if err=pc.SetLocalDescription(offer);err!=nil{r.Error=err.Error();return}
 select{case <-gather:case <-time.After(10*time.Second):r.Error="ICE gathering timed out";return}
 send("offer",pc.LocalDescription())
 done:=make(chan struct{});go func(){defer close(done);var pending []webrtc.ICECandidateInit
  for{var msg message;if ws.ReadJSON(&msg)!=nil{return};switch msg.Event{case "answer":var desc webrtc.SessionDescription;if json.Unmarshal(msg.Data,&desc)==nil{_=pc.SetRemoteDescription(desc);for _,c:=range pending{_=pc.AddICECandidate(c)};pending=nil};case "candidate":var c webrtc.ICECandidateInit;if json.Unmarshal(msg.Data,&c)==nil{if pc.RemoteDescription()==nil{pending=append(pending,c)}else{_=pc.AddICECandidate(c)}};case "error":stats.Lock();r.Error=string(msg.Data);stats.Unlock();return}}
 }()
 select{case <-time.After(time.Duration(seconds)*time.Second):case <-done:}
 _=pc.Close();_=ws.Close();<-done;trackWG.Wait();stats.Lock();defer stats.Unlock();return
}
