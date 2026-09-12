package proto

import (
	"encoding/json"
	"testing"
)

func TestUSBCompositionRequiresExplicitBooleans(t *testing.T) {
	complete := `{"keyboard":false,"relative":false,"absolute":false,"network":true,"disk":false,"serial":true,"audio":false,"mode":"normal","revision":"current"}`
	var req SetUSBCompositionReq
	if err := json.Unmarshal([]byte(complete), &req); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRequest(&req); err != nil {
		t.Fatalf("explicit false rejected: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(complete), &fields); err != nil {
		t.Fatal(err)
	}
	for name := range fields {
		t.Run(name, func(t *testing.T) {
			original := fields[name]
			delete(fields, name)
			defer func() { fields[name] = original }()
			data, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			var partial SetUSBCompositionReq
			if err := json.Unmarshal(data, &partial); err != nil {
				t.Fatal(err)
			}
			if err := ValidateRequest(&partial); err == nil {
				t.Fatalf("omitted %s accepted", name)
			}
		})
	}
}
