package osupdate

import (
	"strings"
	"testing"
)

func TestPreparedReceiptIgnoresFutureHelperFields(t *testing.T) {
	id := strings.Repeat("a", 64)
	receipt, err := DecodePreparedReceipt(strings.NewReader(`{"id":"` + id + `","version":"1.0.0-beta.6","sequence":12,"reboot":true,"future_manifest":{"format":99}}`))
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ID != id || receipt.Sequence != 12 || !receipt.Reboot {
		t.Fatalf("wrong receipt: %#v", receipt)
	}
}

func TestPreparedReceiptRejectsInvalidIdentity(t *testing.T) {
	if _, err := DecodePreparedReceipt(strings.NewReader(`{"id":"../package","version":"1.0.0"}`)); err == nil {
		t.Fatal("accepted invalid package identifier")
	}
}
