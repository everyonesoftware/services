package main

import (
	"bytes"
	"image/png"
	"os"
	"os/exec"
	"testing"

	"services/internal/svgicon"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == svgicon.WorkerFlag {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func TestSVGWorkerModeBeforeApplicationStartup(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`<rect width="10" height="5" fill="red"/>`, `<script/>`} {
		cmd := exec.Command(exe, svgicon.WorkerFlag)
		cmd.Env = []string{"SystemRoot=" + os.Getenv("SystemRoot"), "LISTEN_ADDR=invalid", "DOCKER_SOCKET=missing", "SERVICES_DATA_DIR=not-writable"}
		cmd.Stdin = bytes.NewBufferString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 5">` + body + `</svg>`)
		out, err := cmd.Output()
		if body == `<script/>` {
			if err == nil || len(out) != 0 {
				t.Fatal("malicious worker input succeeded")
			}
			continue
		}
		if err != nil {
			t.Fatalf("worker mode: %v", err)
		}
		im, err := png.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatal(err)
		}
		if im.Bounds().Dx() != 10 || im.Bounds().Dy() != 5 {
			t.Fatal(im.Bounds())
		}
	}
}
