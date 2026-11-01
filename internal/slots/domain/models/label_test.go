package models

import (
	"encoding/json"
	"testing"
)

func TestSlotLabelsNormalizeExistingNumbersWithoutChangingStoredData(t *testing.T) {
	for _, label := range []string{"Espacio 1", "Espacio N° 1", "espacio 1"} {
		s := Slot{ID: 1, Label: label, PublicID: "1493790d-6574-4a92-8445-afd174027bc0"}
		raw, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]any
		if err = json.Unmarshal(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded["label"] != "Espacio N° 1" || s.Label != label || decoded["public_id"] != s.PublicID {
			t.Fatal("wrong display or changed identifiers", string(raw))
		}
	}
	if got := (Slot{Label: "Bahía norte"}).DisplayLabel(); got != "Bahía norte" {
		t.Fatal("custom label overwritten", got)
	}
}
