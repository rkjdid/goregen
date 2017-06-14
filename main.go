package main

import (
	"flag"
	"fmt"
	"github.com/rkjdid/util"
	"github.com/solar3s/goregen/regenbox"
	"github.com/solar3s/goregen/web"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"time"
)

var (
	conn   *regenbox.SerialConnection
	server *web.Server
	rbCfg  regenbox.Config
	static string
)

var (
	device  = flag.String("dev", "", "path to serial port, if empty it will be searched automatically")
	root    = flag.String("root", "~/.goregen", "path to goregen's config files")
	cfg     = flag.String("config", "", "path to config, defaults to <root>/config.toml")
	verbose = flag.Bool("verbose", false, "higher verbosity")
	version = flag.Bool("version", false, "print version & exit")
	debug   = flag.Bool("debug", false, "enable debug mode")
	assets  = flag.Bool("assets", false, "extract static assets to <root>/static, if true, extracted assets "+
		"also take precedence over binary assets\n\tthis option is useful for doing live tests on front-end")
)

func UserHomeDir() string {
	if runtime.GOOS == "windows" {
		home := os.Getenv("HOMEDRIVE") + os.Getenv("HOMEPATH")
		if home == "" {
			home = os.Getenv("USERPROFILE")
		}
		return home
	}
	return os.Getenv("HOME")
}

func init() {
	flag.Parse()
	if *version {
		fmt.Printf("goregen %s\n", Version)
		os.Exit(0)
	}

	if *device != "" {
		port, config, err := regenbox.OpenPortName(*device)
		if err != nil {
			log.Fatal("error opening serial port: ", err)
		}
		conn = regenbox.NewSerial(port, config, *device)
		conn.Start()
	}

	if *root == "" || *root == "~/.goregen" {
		*root = filepath.Join(UserHomeDir(), ".goregen")
	}
	for _, v := range []string{*root} {
		err := os.MkdirAll(v, 0755)
		if err != nil {
			log.Fatalf("couldn't mkdir \"%s\": %s", v, err)
		}
	}

	if *cfg == "" {
		*cfg = filepath.Join(*root, "config.toml")
	}

	rbCfg = regenbox.DefaultConfig
	err := util.ReadTomlFile(&rbCfg, *cfg)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Fatalf("error reading config \"%s\": %s", *cfg, err)
		}
		err = util.WriteTomlFile(rbCfg, *cfg)
		if err != nil {
			log.Fatalf("error creating config \"%s\": %s", *cfg, err)
		}
		log.Printf("created new config file \"%s\"", *cfg)
	}

	// restore static assets
	if *assets {
		static = filepath.Join(*root, "static")
		err = web.RestoreAssets(*root, "static")
		if err != nil {
			log.Fatalf("couldn't restore static assets in \"%s\": %s", static, err)
		}
	}

	log.Printf("using config file: %s", *cfg)
}

func main() {
	rbox, err := regenbox.NewRegenBox(conn, &rbCfg)
	if err != nil {
		log.Println("error initializing regenbox connection:", err)
	}

	watcher := regenbox.NewWatcher(rbox, regenbox.DefaultWatcherConfig)
	watcher.WatchConn()

	server = &web.Server{
		ListenAddr: "localhost:3636",
		Regenbox:   rbox,
		Verbose:    *verbose,
		Debug:      *debug,
		RboxConfig: *cfg,
		RootDir:    *root,
		StaticDir:  static,
		WsInterval: time.Second * 5,
		Version:    Version,
	}
	server.Start()

	trap := make(chan os.Signal)
	signal.Notify(trap, os.Kill, os.Interrupt)
	<-trap
	fmt.Println()
	log.Println("quit received...")

	cleanExit := make(chan struct{})
	go func() {
		watcher.Stop()
		rbox.Stop()
		rbox.Conn.Close()

		close(cleanExit)
	}()
	select {
	case <-time.After(time.Second * 10):
		log.Panicln("no clean exit after 10sec, please report panic log to https://github.com/solar3s/goregen/issues")
	case <-cleanExit:
	}
}
