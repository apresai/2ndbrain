package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/spf13/cobra"
)

var aiSetupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Guided local AI setup with Ollama",
	Long:  "Checks for Ollama, pulls required models, configures the vault for local AI, and generates embeddings.",
	RunE:  runAISetup,
}

func init() {
	aiCmd.AddCommand(aiSetupCmd)
}

func runAISetup(cmd *cobra.Command, args []string) error {
	// Step 1: Check if Ollama is installed
	fmt.Println("Checking for Ollama...")
	ollamaPath, err := exec.LookPath("ollama")
	if err != nil {
		fmt.Println("\nOllama is not installed. Install it with:")
		fmt.Println("  brew install ollama")
		fmt.Println("\nThen start the server:")
		fmt.Println("  ollama serve")
		fmt.Println("\nRe-run this command after installation.")
		return fmt.Errorf("ollama not found")
	}
	fmt.Printf("  Found: %s\n", ollamaPath)

	// Step 2: Check if Ollama is running
	fmt.Println("\nChecking Ollama server...")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost:11434/", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Println("\nOllama is not running. Start it with:")
		fmt.Println("  ollama serve")
		fmt.Println("\nRe-run this command after starting the server.")
		return fmt.Errorf("ollama not running")
	}
	resp.Body.Close()
	fmt.Println("  Server is running.")

	// Step 3: Pull required models
	embedModel := "embeddinggemma"
	genModel := "gemma3:4b"

	fmt.Printf("\nPulling embedding model: %s\n", embedModel)
	if err := runOllamaPull(embedModel); err != nil {
		return fmt.Errorf("pull %s: %w", embedModel, err)
	}

	fmt.Printf("\nPulling generation model: %s\n", genModel)
	if err := runOllamaPull(genModel); err != nil {
		return fmt.Errorf("pull %s: %w", genModel, err)
	}

	// Step 4: Configure vault
	fmt.Println("\nConfiguring vault for local AI...")
	v, err := openVault()
	if err != nil {
		return err
	}

	v.Config.AI.Provider = "ollama"
	v.Config.AI.EmbeddingModel = embedModel
	v.Config.AI.GenerationModel = genModel
	v.Config.AI.Dimensions = 768
	if err := v.Config.Save(v.DotDir); err != nil {
		v.Close()
		return fmt.Errorf("save config: %w", err)
	}
	v.Close()
	fmt.Println("  Provider: ollama")
	fmt.Println("  Embedding model: " + embedModel)
	fmt.Println("  Generation model: " + genModel)
	fmt.Println("  Dimensions: 768")

	// Step 5: Generate embeddings
	fmt.Println("\nGenerating embeddings (this may take a moment)...")
	indexCmd := exec.Command(os.Args[0], "index")
	indexCmd.Stdout = os.Stdout
	indexCmd.Stderr = os.Stderr
	if err := indexCmd.Run(); err != nil {
		return fmt.Errorf("index: %w", err)
	}

	// Step 6: Summary
	fmt.Println("\nLocal AI setup complete!")
	fmt.Println("  Run `2nb ai status` to verify")
	fmt.Println("  Run `2nb ask \"your question\"` to query your vault")

	return nil
}

func runOllamaPull(model string) error {
	cmd := exec.Command("ollama", "pull", model)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
