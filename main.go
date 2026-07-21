package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"
)

func main() {
	// Machine-readable CLI mode (used by the Decky plugin) when the first arg
	// is a known subcommand; otherwise fall through to serving the web UI.
	if len(os.Args) > 1 && isCLICommand(os.Args[1]) {
		os.Exit(runCLI(os.Args[1:], os.Stdout))
	}

	portFlag := flag.Int("port", 0, "port to listen on (overrides config)")
	noOpen := flag.Bool("no-open", false, "do not open the browser on start")
	flag.Parse()

	cfg, err := LoadConfig()
	if err != nil {
		log.Fatalf("save-sync: %v", err)
	}
	if *portFlag != 0 {
		cfg.Port = *portFlag
	}

	srv := &Server{cfg: cfg}
	addr := fmt.Sprintf("127.0.0.1:%d", cfg.Port)
	url := "http://" + addr

	httpSrv := &http.Server{Addr: addr, Handler: srv.routes()}

	fmt.Printf("save-sync running — open %s\n", url)
	if !*noOpen {
		go func() {
			time.Sleep(400 * time.Millisecond)
			openBrowser(url)
		}()
	}

	if err := httpSrv.ListenAndServe(); err != nil {
		log.Fatalf("save-sync: %v", err)
	}
}

// openBrowser best-effort opens the UI in the default browser.
func openBrowser(url string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "windows":
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	case "darwin":
		cmd, args = "open", []string{url}
	default: // linux, incl. Steam Deck
		cmd, args = "xdg-open", []string{url}
	}
	_ = exec.Command(cmd, args...).Start()
}
