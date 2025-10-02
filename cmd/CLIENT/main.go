package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	// Import the new client library
	"github.com/1amkhush/torrentium/pkg/torrentium_client"

	db "github.com/1amkhush/torrentium/pkg/db"
	p2p "github.com/1amkhush/torrentium/pkg/p2p"

	//"github.com/joho/godotenv"
	"github.com/libp2p/go-libp2p/core/host"
)

// The commandLoop now takes the client as an argument
func commandLoop(c *torrentium_client.Client) {
	scanner := bufio.NewScanner(os.Stdin)
	printInstructions() // You can move printInstructions to this file as well
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}
		parts := strings.Fields(scanner.Text())
		if len(parts) == 0 {
			continue
		}
		cmd, args := parts[0], parts[1:]
		var err error
		switch cmd {
		case "help":
			printInstructions()
		case "add":
			if len(args) != 1 {
				fmt.Println("Usage: add <path>")
			} else {
				// Call the library function
				err = c.AddFile(args[0])
			}
		case "list":
			c.ListLocalFiles()
		case "search":
			if len(args) != 1 {
				fmt.Println("Usage: search <cid|text>")
			} else {
				if strings.HasPrefix(args[0], "bafy") || strings.HasPrefix(args[0], "Qm") {
					err = c.EnhancedSearchByCID(args[0])
				} else {
					err = c.SearchByText(args[0])
				}
			}
		case "download":
			if len(args) != 1 {
				fmt.Println("Usage: download <cid>")
			} else {
				err = c.DownloadFile(args[0])
			}
		case "peers":
			c.ListConnectedPeers()
		case "health":
			c.CheckConnectionHealth()
		case "debug":
			c.DebugNetworkStatus()
		case "exit":
			return
		default:
			fmt.Println("Unknown command. Type 'help' for available commands.")
		}
		if err != nil {
			log.Printf("Error: %v", err)
		}
	}
}

func printInstructions() {
	fmt.Println("\nAvailable Commands:")
	fmt.Println("  add <path>       - Share a file on the network")
	fmt.Println("  list             - List your shared files")
	fmt.Println("  search <cid|text>- Search by CID or filename text")
	fmt.Println("  download <cid>   - Download a file by CID")
	fmt.Println("  peers            - Show connected peers")
	fmt.Println("  help             - Show this help")
	fmt.Println("  exit             - Exit the application")
}

func setupGracefulShutdown(h host.Host) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-ch
		log.Println("Shutting down gracefully...")
		_ = h.Close()
		os.Exit(0)
	}()
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// if err := godotenv.Load(); err != nil {
	// 	//log.Printf("Warning: Could not load .env file: %v", err)
	// }

	DB := db.InitDB()
	if DB == nil {
		log.Fatal("Database initialization failed")
	}

	h, d, err := p2p.NewHost(
		ctx,
		"/ip4/0.0.0.0/tcp/0",
		nil,
	)
	if err != nil {
		log.Fatal("Failed to create libp2p host:", err)
	}
	defer h.Close()

	go func() {
		if err := p2p.Bootstrap(ctx, h, d); err != nil {
			log.Printf("Error bootstrapping DHT: %v", err)
		}
	}()

	setupGracefulShutdown(h)

	repo := db.NewRepository(DB)

	// Create a new client from the library
	client := torrentium_client.NewClient(h, d, repo)

	// Start background tasks
	client.StartDHTMaintenance()

	// Run the command loop
	commandLoop(client)
}
