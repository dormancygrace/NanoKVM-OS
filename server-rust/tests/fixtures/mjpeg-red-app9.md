Synthetic test-only64x48 RGB(220,30,40) JPEG, pinned Go1.27.1 standard image/jpeg encoder Quality90, generated 2026-10-05T15:12:13.854722+00:00. Exact APP9 counter inserted after SOI; SHA256 8d3e85e8d02885c3cd52c1698cec2833a7b7749166ca513e84aca22610226140. Go used only to create checked-in static test bytes, never runtime/package. Native fixture varies only APP9 counter; no device encoding qualification.

Generator:
```go
package main
import("os";"image";"image/color";"image/jpeg";"bytes")
func main(){img:=image.NewRGBA(image.Rect(0,0,64,48));for y:=0;y<48;y++{for x:=0;x<64;x++{img.Set(x,y,color.RGBA{220,30,40,255})}};var out bytes.Buffer;if err:=jpeg.Encode(&out,img,&jpeg.Options{Quality:90});err!=nil{panic(err)};data:=out.Bytes();fixture:=append([]byte{0xff,0xd8,0xff,0xe9,0,4,0,1},data[2:]...);if err:=os.WriteFile(os.Args[1],fixture,0600);err!=nil{panic(err)}}
```
