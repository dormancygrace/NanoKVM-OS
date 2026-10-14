#!/usr/bin/env python3
from pathlib import Path
import os,subprocess,tempfile
"""Check production video bindings against an ABI that exits on every native call."""
r=Path(__file__).resolve().parents[1]
temporary=tempfile.TemporaryDirectory(prefix='nanokvm-video-guard-')
o=Path(temporary.name)
(o/'main.go').write_text('''package main
import("os";"fmt";"NanoKVM-Server/common")
func main(){
 if len(os.Args)==1{common.DisableVideo()}
 k:=common.GetKvmVision()
 if k.SetHDMI(true)!=-1{panic("HDMI was not refused")}
 if k.HasHDMISignal(){panic("unexpected signal")}
 k.SetGop(30); k.SetFrameDetect(0); k.RequestKeyframe()
 if k.SetGOPMode(0)!=-1||k.SetMjpegChroma(422)!=-1{panic("native setting accepted")}
 if k.ApplyMonitorProfile("unused")==nil{panic("monitor write accepted")}
 if k.StartVideoCapture(common.VideoCaptureParams{})!=nil{panic("capture started")}
 if _,result:=k.ReadMjpeg(640,480,80);result>=0{panic("JPEG read accepted")}
 if _,_,result:=k.ReadVideoWithHeadroom(640,480,1,3000,30,30,0);result>=0{panic("video read accepted")}
 common.GetActiveGOPMode();common.GetMjpegChromaStatus();common.GetCaptureScreen();k.Close()
 fmt.Println("PASS real native bindings: recovery touches no video ABI")
}
''')
symbols=['kvmv_init','kvmv_deinit','kvmv_hdmi_control','kvmv_hdmi_signal_active','kvmv_request_keyframe','kvmv_read_mjpeg_sink','kvmv_read_video_sink','kvmv_read_img','kvmv_set_capture_fps','set_h264_gop','set_h265_gop_mode','get_h265_gop_mode','set_frame_detact','set_mjpeg_chroma','get_mjpeg_chroma_status']
(o/'trap.c').write_text('#include <unistd.h>\n'+''.join('void '+s+'(void){_exit(99);}\n' for s in symbols))
subprocess.run(['gcc','-shared','-fPIC',str(o/'trap.c'),'-o',str(o/'libkvm.so')],check=True)
go=os.environ.get('GO','go')
env={**os.environ,'GOTOOLCHAIN':'local','CGO_ENABLED':'1','CC':'gcc','CGO_LDFLAGS':f'-L{o} -Wl,-rpath-link,{o}'}
subprocess.run([str(go),'build','-tags=nomsgpack','-o',str(o/'check'),str(o/'main.go')],cwd=r/'server',env=env,check=True)
cmd=[str(o/'check')]
subprocess.run(cmd,env={**env,'LD_LIBRARY_PATH':str(o)},check=True,timeout=30)
control=subprocess.run([*cmd,'normal'],env={**env,'LD_LIBRARY_PATH':str(o)},timeout=30)
assert control.returncode==99,control.returncode
print('PASS positive control: normal startup calls the trapped native ABI')

temporary.cleanup()
