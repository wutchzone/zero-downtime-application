package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func main() {
	log.Println("Started HTTP API, PID: ", os.Getpid())
	var l net.Listener

	// Try to obtain parent's listener and kill him.
	if fd, err := listener(); err != nil {
		log.Println("Parent does not exists, starting a normal way")
		l, err = net.Listen("tcp", ":8080")

		if err != nil {
			panic(err)
		}
	} else {
		l = fd
		killParent()
		time.Sleep(time.Second)
	}

	// Start the web server.
	s := &http.Server{}
	http.HandleFunc("/", func(rw http.ResponseWriter, r *http.Request) {
		log.Printf("New request! From: %d, path: %s, method: %s: ", os.Getpid(),
			r.URL, r.Method)
	})
	go s.Serve(l)

	// Start loop which is responsible for upgrade watching.
	upgradeLoop(&l, s)
}

func upgradeLoop(l *net.Listener, s *http.Server) {
	sig := make(chan os.Signal)
	signal.Notify(sig, syscall.SIGQUIT, syscall.SIGUSR2)
	for t := range sig {
		switch t {
		case syscall.SIGUSR2:
			log.Println("Received SIGUSR2 upgrading binary")
			// Fork a child and start binary upgrading.
			if err := spawnChild(); err != nil {
				log.Println(
					"Cannot perform binary upgrade, when starting process: ",
					err.Error(),
				)
				continue
			}
		case syscall.SIGQUIT:
			s.Shutdown(context.Background())
			os.Exit(0)
		}
	}
}

func listener() (net.Listener, error) {
	lc := net.ListenConfig{
		Control: control,
	}
	if l, err := lc.Listen(context.TODO(), "tcp", ":8080"); err != nil {
		return nil, err
	} else {
		return l, nil
	}
}

// When parent process exists, send it signals, that it should perform graceful
// shutdown and stop serving new requests.
func killParent() error {
	ppid, err := strconv.Atoi(os.Getenv("APP_PPID"))
	if err != nil {
		return err
	}

	if p, err := os.FindProcess(ppid); err != nil {
		return err
	} else {
		return p.Signal(syscall.SIGQUIT)
	}
}

func spawnChild() error {
	argv0, err := exec.LookPath(os.Args[0])
	if err != nil {
		return err
	}

	wd, err := os.Getwd()
	if err != nil {
		return err
	}

	files := make([]*os.File, 0)
	files = append(files, os.Stdin, os.Stdout, os.Stderr)

	ppid := os.Getpid()
	os.Setenv("APP_PPID", strconv.Itoa(ppid))

	os.StartProcess(argv0, os.Args, &os.ProcAttr{
		Dir:   wd,
		Env:   os.Environ(),
		Files: files,
		Sys:   &syscall.SysProcAttr{},
	})

	return nil
}

func control(network, address string, c syscall.RawConn) error {
	var err error
	c.Control(func(fd uintptr) {
		err = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
		if err != nil {
			return
		}

		err = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
		if err != nil {
			return
		}
	})
	return err
}
