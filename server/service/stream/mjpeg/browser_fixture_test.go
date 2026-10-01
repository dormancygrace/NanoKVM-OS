package mjpeg

import (
	"bytes"
	"fmt"
	"github.com/gin-gonic/gin"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"os"
	"testing"
	"time"
)

// Opt-in, loopback-only fixture exercising the production writer with generated
// JPEGs. No capture/device state is accessed. Stop the test process after QA.
func TestMjpegPaintBrowserFixture(t *testing.T) {
	addr := os.Getenv("NANOKVM_MJPEG_FIXTURE_ADDR")
	if addr == "" {
		t.Skip("set NANOKVM_MJPEG_FIXTURE_ADDR=127.0.0.1:port for Chrome Main QA")
	}
	if addr != "127.0.0.1:18747" {
		t.Fatal("fixture must use its dedicated loopback address")
	}
	var frames [][]byte
	for _, c := range []color.RGBA{{230, 30, 30, 255}, {30, 210, 40, 255}, {30, 50, 230, 255}} {
		img := image.NewRGBA(image.Rect(0, 0, 320, 120))
		for y := 0; y < 120; y++ {
			for x := 0; x < 320; x++ {
				img.SetRGBA(x, y, c)
			}
		}
		var b bytes.Buffer
		if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 90}); err != nil {
			t.Fatal(err)
		}
		frames = append(frames, b.Bytes())
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><html><head><meta charset="utf-8"><title>MJPEG paint regression</title><style>body{font:16px system-ui;padding:25px;background:#f4f7fa;color:#173043}section{display:inline-block;vertical-align:top;margin:15px;padding:18px;background:white;border:1px solid #cad6df}img{display:block;width:320px;height:120px;background:#bbb}button{font-size:16px;padding:12px}pre{font-size:13px;white-space:pre-wrap;max-width:900px}h2{font-size:18px}</style></head><body><h1>MJPEG paint regression</h1><p>Generated JPEGs. Red → green → blue, then silence for 10 seconds. The single-frame case stays blue.</p><button id="start">Start comparison</button><div><section><h2>Legacy multipart</h2><img id="legacy" alt="Legacy MJPEG"><output id="legacy-status">Waiting</output></section><section><h2>Advance headers</h2><img id="advance" alt="Advance MJPEG"><output id="advance-status">Waiting</output></section><section><h2>Single static frame</h2><img id="single" alt="Single-frame MJPEG"><output id="single-status">Waiting</output></section></div><pre role="status" aria-label="Paint evidence" id="evidence">Waiting for test</pre><script>
let started=0,observed={},timer;
const ids=['legacy','advance','single'];
document.getElementById('start').onclick=()=>{
 started=performance.now();observed={};clearInterval(timer);
 for(const id of ids){observed[id]=[];document.getElementById(id+'-status').textContent='Waiting';document.getElementById(id).src='/stream/'+id+'?run='+Date.now();}
 timer=setInterval(()=>{
  for(const id of ids){const img=document.getElementById(id);let name='waiting';
   if(img.naturalWidth){try{const c=document.createElement('canvas');c.width=1;c.height=1;const ctx=c.getContext('2d');ctx.drawImage(img,0,0,1,1);const p=ctx.getImageData(0,0,1,1).data;name=p[0]>150?'red':p[1]>150?'green':p[2]>150?'blue':'other';}catch(e){name='error';}}
   const list=observed[id];if(name!='waiting'&&(!list.length||list[list.length-1].color!=name)){list.push({color:name,ms:Math.round(performance.now()-started)});}
   document.getElementById(id+'-status').textContent=name;
  }
  document.getElementById('evidence').textContent=JSON.stringify({elapsedMs:Math.round(performance.now()-started),observed},null,2);
 },50);
};
</script></body></html>`)
	})
	mux.HandleFunc("/stream/", func(w http.ResponseWriter, r *http.Request) {
		mode := r.URL.Path[len("/stream/"):]
		if mode != "legacy" && mode != "advance" && mode != "single" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
		w.Header().Set("Cache-Control", "no-store")
		c, _ := gin.CreateTestContext(w)
		c.Request = r
		controller := newResponseController(c.Writer)
		images := frames
		if mode == "single" {
			images = frames[2:]
		}
		for i, data := range images {
			if i > 0 {
				select {
				case <-time.After(750 * time.Millisecond):
				case <-r.Context().Done():
					return
				}
			}
			if mode == "legacy" {
				fmt.Fprintf(w, "--frame\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", len(data))
				w.Write(data)
				w.Write([]byte("\r\n"))
				controller.Flush()
			} else if err := writeFrame(c, controller, data, i == 0); err != nil {
				return
			}
		}
		select {
		case <-time.After(10 * time.Second):
		case <-r.Context().Done():
			return
		}
	})
	t.Log("Chrome Main fixture at http://" + addr)
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 3 * time.Second}
	t.Fatal(server.ListenAndServe())
}
