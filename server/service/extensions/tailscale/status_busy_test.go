package tailscale

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"NanoKVM-Server/proto"
)

func TestStatusSaysWhenAnOperationRuns(t *testing.T) {
	gin.SetMode(gin.TestMode)
	busy := func() bool {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/extensions/tailscale/status", nil)
		(&Service{}).GetStatus(c)
		var rsp struct {
			Data struct {
				Busy bool `json:"busy"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &rsp); err != nil {
			t.Fatal(err)
		}
		return rsp.Data.Busy
	}
	if busy() {
		t.Fatal("busy with no operation")
	}
	var rsp proto.Response
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if !beginOperation(c, &rsp) {
		t.Fatal("no operation could begin")
	}
	got := busy()
	endOperation()
	if !got {
		t.Fatal("an install in progress was not reported")
	}
	// The status route reads a flag: polling it never takes the lock a
	// click needs.
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				busy()
			}
		}
	}()
	for range 200 {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		if !beginOperation(c, &rsp) {
			close(stop)
			<-done
			t.Fatal("a status poll made an operation look busy")
		}
		endOperation()
	}
	close(stop)
	<-done
}
