package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

func main() {
	serviceName := os.Getenv("SERVICE_NAME")
	if serviceName == "" {
		serviceName = "dummy-service"
	}

	servicesDir := os.Getenv("NAUTROUDS_SERVICES_DIR")
	if servicesDir == "" {
		servicesDir = "/var/run/nautrouds/services"
	}

	socketDir := filepath.Join(servicesDir, serviceName)
	if err := os.MkdirAll(socketDir, 0755); err != nil {
		log.Fatalf("Failed to create socket directory: %v", err)
	}

	socketPath := filepath.Join(servicesDir, serviceName, "node-0.sock")

	// Cleanup old socket
	if _, err := os.Stat(socketPath); err == nil {
		os.Remove(socketPath)
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Fatalf("Failed to listen on UDS: %v", err)
	}
	defer os.Remove(socketPath)

	// Ensure permissions for core to access
	// We use 0666 here as a safe default because ACLs can be unreliable on some systems.
	os.Chmod(socketPath, 0666)

	log.Printf("Service %s listening on %s", serviceName, socketPath)

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hostname, _ := os.Hostname()
			fmt.Fprintf(w, "Hello from %s (hostname: %s)\n", serviceName, hostname)
			log.Printf("Handled request: %s %s", r.Method, r.URL.Path)
		}),
	}

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	<-sigChan
	log.Println("Shutting down...")
	server.Close()
}
