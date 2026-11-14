package vm

import (
	"time"

	"github.com/gin-gonic/gin"

	"NanoKVM-Server/journal"
	"NanoKVM-Server/proto"
	"NanoKVM-Server/service/notify"
)

// GetJournal returns the event journal, newest first, in the words of the
// page's language (?lang=).
func (s *Service) GetJournal(c *gin.Context) {
	type entry struct {
		Seq      uint64    `json:"seq"`
		Time     time.Time `json:"time"`
		Kind     string    `json:"kind"`
		Severity string    `json:"severity"`
		Title    string    `json:"title"`
		Body     string    `json:"body"`
	}
	entries := []entry{}
	for _, e := range journal.Default.Entries(0) {
		title, body := notify.Describe(c.Query("lang"), e)
		entries = append(entries, entry{Seq: e.Seq, Time: e.Time, Kind: e.Kind, Severity: e.Severity, Title: title, Body: body})
	}
	c.Header("Cache-Control", "no-store")
	var rsp proto.Response
	rsp.OkRspWithData(c, gin.H{"entries": entries, "kept": journal.Max})
}
