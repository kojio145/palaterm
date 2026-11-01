package model

import (
	"encoding/json"
	"strings"
	"testing"
)

// M-12: Settings.TileKeepMain is stored as "tileKeepMain", left out while
// off (a vault from before the setting reads back off).
func TestReview1101U2TileKeepMainJSON(t *testing.T) {
	var s Settings
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "tileKeepMain") {
		t.Errorf("オフの設定が書かれた: %s", b)
	}
	s.TileKeepMain = true
	b, _ = json.Marshal(s)
	if !strings.Contains(string(b), `"tileKeepMain":true`) {
		t.Errorf("オンの設定が書かれない: %s", b)
	}
	var back Settings
	if err := json.Unmarshal([]byte(`{"maxParallel":4,"tileKeepMain":true}`), &back); err != nil || !back.TileKeepMain {
		t.Errorf("読み戻し: %v %+v", err, back)
	}
	back = Settings{}
	if err := json.Unmarshal([]byte(`{"maxParallel":4}`), &back); err != nil || back.TileKeepMain {
		t.Errorf("古い vault はオフ: %v %+v", err, back)
	}
}
