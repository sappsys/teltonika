// usb_programmer programs Teltonika trackers over USB CDC using the
// Configurator text + FMBX protocol (experimental).
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/sappsys/teltonika/experimental/usb_programmer/usb"
)

func main() {
	configPath := flag.String("config", "", "config file: .cfg (Configurator gzip) or plain text (.txt)")
	rootPath := flag.String("root", "", "optional root CA PEM")
	keyPath := flag.String("key", "", "optional private key PEM")
	certPath := flag.String("cert", "", "optional device certificate PEM")
	port := flag.String("port", "", "serial port (default: probe USB candidates)")
	clearCerts := flag.Bool("clear-certs", false, "delete TLS PEMs (also done before cert upload)")
	skipReset := flag.Bool("skip-reset", false, "do not factory-reset before programming")
	stepTimeout := flag.Duration("timeout", usb.DefaultStepTimeout, "hard deadline per programming step")
	retries := flag.Int("retries", 0, "restart from the beginning N times on failure")
	verbose := flag.Bool("v", false, "verbose protocol trace + diagnostics on stderr")
	flag.Parse()

	progress := func(msg string) { fmt.Println(msg) }
	fail := func(reason string) {
		fmt.Printf("Status Fail (%s)\n", reason)
		os.Exit(1)
	}

	certsWanted := *rootPath != "" || *keyPath != "" || *certPath != ""
	if *configPath == "" && !*clearCerts {
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
  --timeout DUR       per-step deadline (default 20s)
  --retries N         full restarts on failure
  -v                  verbose

WARNING: writes device configuration. Tested only on FMB020 and FMC920.
See README.md in this directory.

`)
		os.Exit(2)
	}
	if *retries < 0 {
		fail("--retries must be >= 0")
	}

	opt := usb.ProgramOptions{
		SkipReset:   *skipReset,
		ClearCerts:  *clearCerts || certsWanted,
		StepTimeout: *stepTimeout,
		Progress:    progress,
	}
	if *verbose {
		opt.Verbose = func(msg string) { fmt.Fprintln(os.Stderr, msg) }
	}

	if *configPath != "" {
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
		if *verbose {
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
	type result struct {
		port string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		port, _, err := usb.FindTracker(prefer)
		ch <- result{port, err}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-ch:
		return r.port, r.err
	case <-timer.C:
		return "", &usb.StepTimeoutError{Step: "Waiting for device", Timeout: timeout}
	}
}
