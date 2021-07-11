package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"
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
			f, err := (*l).(*net.TCPListener).File()
			if err != nil {
				log.Println(
					"Cannot perform binary upgrade,",
					" when getting file descriptor: ",
					err.Error())
				continue
			}

			if err := spawnChild(int(f.Fd())); err != nil {
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

func listener() (l net.Listener, err error) {
	fd, err := strconv.Atoi(os.Getenv("APP_FD"))
	if err != nil {
		return nil, err
	}
	l, err = net.FileListener(os.NewFile(uintptr(fd), os.Getenv("GOAGAIN_NAME")))
	if nil != err {
		return
	}
	switch l.(type) {
	case *net.TCPListener, *net.UnixListener:
	default:
		err = fmt.Errorf(
			"file descriptor is %T not *net.TCPListener or *net.UnixListener",
			l,
		)
		return
	}
	if err = syscall.Close(int(fd)); nil != err {
		return
	}
	return
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

func spawnChild(fd int) error {
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

	if fd := os.NewFile(uintptr(fd), "listener"); fd != nil {
		files = append(files, fd)
	} else {
		// This may happen when our fd is invalid number.
		// Invalid is when the fd is not opened.
		return errors.New("Invalid FD")
	}

	// Clear all flags from the sockets, namely FD_CLOEXEC
	_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd),
		syscall.F_SETFD, 0)
	if errno != 0 {
		return errors.New("Could not set FD options")
	}

	ppid := os.Getpid()
	os.Setenv("APP_PPID", strconv.Itoa(ppid))
	os.Setenv("APP_FD", strconv.Itoa(int(fd)))

	os.StartProcess(argv0, os.Args, &os.ProcAttr{
		Dir:   wd,
		Env:   os.Environ(),
		Files: files,
		Sys:   &syscall.SysProcAttr{},
	})

	return nil
}
