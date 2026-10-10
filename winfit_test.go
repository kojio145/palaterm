package main

import "testing"

func TestFitSize(t *testing.T) {
	cases := []struct {
		name   string
		sw, sh int
		want   winSize
		w, h   int
	}{
		{"広い画面はそのまま", 1920, 1080, mainWin, 1280, 800},
		{"125% のノート（1536×864）は高さだけ縮む", 1536, 864, mainWin, 1280, 768},
		{"1366×768 は最小高さで止まる", 1366, 768, mainWin, 1280, 680},
		{"1280×720 は最小値まで", 1280, 720, mainWin, 1256, 680},
		{"差分窓も同じ規則", 1366, 768, diffWin, 1320, 672},
		{"画面不明（0）は既定", 0, 0, termWin, 1080, 680},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, h := fitSize(c.sw, c.sh, c.want)
			if w != c.w || h != c.h {
				t.Fatalf("fitSize(%d, %d, %+v) = %d×%d, want %d×%d", c.sw, c.sh, c.want, w, h, c.w, c.h)
			}
		})
	}
}
