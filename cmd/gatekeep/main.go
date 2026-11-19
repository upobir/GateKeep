package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gatekeep/internal/db"
	"gatekeep/internal/notify"
	"gatekeep/internal/server"
	"gatekeep/internal/tray"
)

func main() {
	port := flag.Int("port", 8765, "listen port on 127.0.0.1")
	dbFlag := flag.String("db", "", "sqlite database path")
	flag.Parse()

	if *port < 1 || *port > 65535 {
		log.Fatalf("invalid port %d", *port)
	}

	dbPath := *dbFlag
	if dbPath == "" {
		var err error
		dbPath, err = db.DefaultPath()
		if err != nil {
			log.Fatal(err)
		}
	}
	store, err := db.Open(dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	log.Printf("database %s", dbPath)

	srv, err := server.New(*port, store)
	if err != nil {
		log.Fatal(err)
	}
	notify.SetEnabled(loadNotifications(store))
	srv.SetNotifier(notify.Send)
	log.Printf("listening on %s", srv.URL())
	log.Printf("mcp %s/mcp", srv.URL())

	go func() {
		if err := srv.Serve(); err != nil {
			log.Printf("server: %v", err)
			tray.Quit()
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ready := make(chan struct{})
	go func() {
		<-ready
		<-ctx.Done()
		tray.Quit()
	}()

	tray.Run(tray.Options{
		URL: srv.URL(),
		OnReady: func() {
			if notify.Enabled() {
				notify.Start()
			}
			close(ready)
		},
		OnNotifications: func(enabled bool) {
			if err := saveNotifications(store, enabled); err != nil {
				log.Printf("notifications: %v", err)
				return
			}
			notify.SetEnabled(enabled)
			if enabled {
				notify.Start()
			}
		},
		OnQuit: func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := srv.Shutdown(shutdownCtx); err != nil {
				log.Printf("shutdown: %v", err)
			}
			if err := store.Close(); err != nil {
				log.Printf("database: %v", err)
			}
		},
	})
}

func loadNotifications(store *db.Store) bool {
	setting, err := store.GetSetting(context.Background(), "notifications")
	if errors.Is(err, db.ErrNotFound) {
		return true
	}
	if err != nil {
		log.Printf("notifications: %v", err)
		return true
	}
	return setting.Value != "0"
}

func saveNotifications(store *db.Store, enabled bool) error {
	value := "0"
	if enabled {
		value = "1"
	}
	_, err := store.PutSetting(context.Background(), "notifications", value)
	return err
}
