package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"

	"github.com/gatogato999/soapviz/config"
	"github.com/gatogato999/soapviz/web"
)

func main() {
	cfgPath := flag.String("config", "", "path to config.ini (default: ./config.ini, then next to the binary)")
	addr := flag.String("addr", "", "listen address (overrides config)")
	open := flag.Bool("open", false, "open the browser at startup (overrides config)")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("soapviz", buildVersion())
		return
	}

	cfg := loadConfig(*cfgPath)
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "addr":
			cfg.Listen = *addr
		case "open":
			cfg.OpenBrowser = *open
		}
	})

	srv, err := web.New(cfg)
	if err != nil {
		log.Fatal(err)
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		log.Fatal(err)
	}
	url := "http://" + ln.Addr().String()
	fmt.Println("soapviz listening on", url)
	if cfg.OpenBrowser {
		_ = exec.Command("xdg-open", url).Start()
	}
	log.Fatal(http.Serve(ln, srv.Handler()))
}

// loadConfig uses -config if given (any failure is fatal). Otherwise it tries
// ./config.ini and then the binary's directory, falling back to built-in
// defaults only when neither exists. A config file that exists but is invalid
// is always fatal.
func loadConfig(explicit string) config.Config {
	if explicit != "" {
		cfg, err := config.Load(explicit)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println("config:", explicit)
		return cfg
	}
	candidates := []string{"config.ini"}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "config.ini"))
	}
	for _, p := range candidates {
		cfg, err := config.Load(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println("config:", p)
		return cfg
	}
	fmt.Println("config: no config.ini found, using built-in defaults")
	return config.Default()
}

// buildVersion reports the module version when installed with
// "go install ...@vX.Y.Z". Built from a checkout it falls back to the commit.
func buildVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	rev, dirty := "", ""
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value[:min(12, len(s.Value))]
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "-dirty"
			}
		}
	}
	if rev == "" {
		return "devel"
	}
	return "devel+" + rev + dirty
}
