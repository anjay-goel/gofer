package payload

import (
	"compress/gzip"
	"embed"
	"fmt"
	"io"
	"os"
	"runtime"
)

//go:embed all:bin
var files embed.FS

func Binary(goos, arch string) ([]byte, error) {
	if goos == runtime.GOOS && arch == runtime.GOARCH {
		path, err := os.Executable()
		if err != nil {
			return nil, err
		}
		return os.ReadFile(path)
	}
	f, err := files.Open(fmt.Sprintf("bin/gofer-%s-%s.gz", goos, arch))
	if err != nil {
		return nil, fmt.Errorf("this gofer build has no binary for %s/%s. Build it with make", goos, arch)
	}
	defer f.Close()
	r, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}
