// usb_programmer programs Teltonika trackers over USB CDC using the
// Configurator text + FMBX protocol (experimental).
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sappsys/teltonika/experimental/usb_programmer/usb"
)

func main() {
	configPath := flag.String("config", "", "config file: .cfg (Configurator) or plain text (.txt / .cfg.txt)")
	rootPath := flag.String("root", "", "optional root CA PEM")
	keyPath := flag.String("key", "", "optional private key PEM")
	certPath := flag.String("cert", "", "optional device certificate PEM")
	port := flag.String("port", "", "serial port (default: probe USB candidates)")
	clearCerts := flag.Bool("clear-certs", false, "delete TLS PEMs from device (also done before --root/--key/--cert upload)")
	skipReset := flag.Bool("skip-reset", false, "do not factory-reset before programming (debug)")
	noReboot := flag.Bool("no-reboot", false, "do not send .reset after successful programming")
	stepTimeout := flag.Duration("timeout", usb.DefaultStepTimeout, "hard deadline per programming step")
	retries := flag.Int("retries", 0, "on failure, restart from the beginning this many times")
	keywordFlag := flag.String("keyword", "", "Configurator keyword (skip interactive prompt)")
	verbose := flag.Bool("v", false, "verbose: protocol trace + extra diagnostics on stderr")
	flag.Parse()

	progress := func(msg string) {
		fmt.Println(msg)
	}
	fail := func(reason string) {
		fmt.Printf("Status Fail (%s)\n", reason)
		os.Exit(1)
	}
	var verboseFn func(string)
	if *verbose {
		verboseFn = func(msg string) {
			fmt.Fprintln(os.Stderr, msg)
		}
	}

	certsWanted := *rootPath != "" || *keyPath != "" || *certPath != ""
	wantConfig := *configPath != ""
	if !wantConfig && !*clearCerts && !certsWanted {
		fmt.Fprintf(os.Stderr, `EXPERIMENTAL — usb_programmer (Teltonika USB Configurator protocol)

Usage:
  usb_programmer --config example/example.txt
  usb_programmer --config file.cfg --root root.pem --key key.pem --cert cert.pem
  usb_programmer --clear-certs

  --config PATH       .cfg or plain-text config (required unless --clear-certs alone)
  --root/--key/--cert TLS PEMs (all three required to upload)
  --clear-certs       delete device TLS PEMs
  --port PATH         serial device (default: auto-probe)
  --skip-reset        skip cfg_default
  --no-reboot         skip .reset after successful programming
  --timeout DUR       per-step deadline (default 20s)
  --retries N         full restarts on failure
  --keyword WORD      Configurator keyword (letters/digits, ≥4; skips prompt)
  -v                  verbose

WARNING: writes device configuration. Tested on FMB020, FMC920, and FMP100.
See README.md in this directory.

`)
		os.Exit(2)
	}
	if *retries < 0 {
		fail("--retries must be >= 0")
	}

	flagKeyword := strings.TrimSpace(*keywordFlag)
	if flagKeyword != "" {
		if err := usb.ValidateKeyword(flagKeyword); err != nil {
			fail("--keyword: " + err.Error())
		}
	}

	cachedKeyword := flagKeyword
	promptKeyword := func() (string, error) {
		if cachedKeyword != "" {
			if flagKeyword != "" && cachedKeyword == flagKeyword {
				fmt.Println("Using --keyword")
			} else {
				fmt.Println("Using cached Configurator keyword")
			}
			return cachedKeyword, nil
		}
		fmt.Print("Configurator keyword (Ctrl-C to abort): ")
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil {
			return "", err
		}
		kw := strings.TrimSpace(line)
		if kw != "" {
			cachedKeyword = kw
		}
		return kw, nil
	}

	opt := usb.ProgramOptions{
		SkipReset:     *skipReset,
		SkipReboot:    *noReboot,
		ClearCerts:    *clearCerts || certsWanted,
		StepTimeout:   *stepTimeout,
		Progress:      progress,
		Verbose:       verboseFn,
		PromptKeyword: promptKeyword,
	}

	if wantConfig {
		doc, err := usb.LoadConfig(*configPath)
		if err != nil {
			fail("config: " + err.Error())
		}
		if len(doc.Params) == 0 {
			fail("no parameters in config")
		}
		opt.Params = make([]usb.Param, 0, len(doc.Params))
		for _, p := range doc.Params {
			opt.Params = append(opt.Params, usb.Param{ID: p.Key, Value: p.Value})
		}
	} else {
		opt.SkipReset = true
	}

	if certsWanted {
		if *rootPath == "" || *keyPath == "" || *certPath == "" {
			fail("cert upload needs all of --root, --key, and --cert")
		}
		var err error
		opt.Root, err = os.ReadFile(*rootPath)
		if err != nil {
			fail("root: " + err.Error())
		}
		opt.Key, err = os.ReadFile(*keyPath)
		if err != nil {
			fail("key: " + err.Error())
		}
		opt.Cert, err = os.ReadFile(*certPath)
		if err != nil {
			fail("cert: " + err.Error())
		}
	}

	attempts := 1 + *retries
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			progress(fmt.Sprintf("Retry %d/%d", attempt-1, *retries))
			time.Sleep(time.Second)
		}

		progress("Waiting for device...")
		portName, err := waitForDevice(*port, *stepTimeout)
		if err != nil {
			lastErr = err
			if attempt < attempts {
				progress(fmt.Sprintf("Status Fail (%s) — will retry", err.Error()))
				continue
			}
			fail(err.Error())
		}
		progress("Device found")

		client, err := usb.Open(portName)
		if err != nil {
			lastErr = fmt.Errorf("open %s: %w", portName, err)
			if attempt < attempts {
				progress(fmt.Sprintf("Status Fail (%s) — will retry", lastErr.Error()))
				continue
			}
			fail(lastErr.Error())
		}
		if verboseFn != nil {
			client.SetLogger(func(format string, args ...any) {
				fmt.Fprintf(os.Stderr, format+"\n", args...)
			})
		}

		result, err := client.Program(opt)
		_ = client.Close()
		if result != nil {
			if s := result.Summary(); s != "" {
				fmt.Println(s)
			}
		}
		if err == nil {
			fmt.Println("Status Complete")
			return
		}
		if strings.Contains(err.Error(), "keyword rejected") {
			cachedKeyword = ""
		}
		lastErr = err
		if attempt < attempts {
			progress(fmt.Sprintf("Status Fail (%s) — will retry", err.Error()))
			continue
		}
	}
	fail(lastErr.Error())
}

func waitForDevice(prefer string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = usb.DefaultStepTimeout
	}
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		port, _, err := usb.FindTracker(prefer)
		if err == nil {
			return port, nil
		}
		lastErr = err
		remaining := time.Until(deadline)
		if remaining <= 0 {
			if lastErr != nil {
				return "", lastErr
			}
			return "", &usb.StepTimeoutError{Step: "Waiting for device", Timeout: timeout}
		}
		sleep := 2 * time.Second
		if sleep > remaining {
			sleep = remaining
		}
		time.Sleep(sleep)
	}
}
