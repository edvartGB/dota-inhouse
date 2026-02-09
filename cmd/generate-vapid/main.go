package main

import (
	"encoding/base64"
	"fmt"
	"log"
	"os"

	webpush "github.com/SherClockHolmes/webpush-go"
)

func main() {
	// Generate VAPID keys
	privateKey, publicKey, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		log.Fatalf("Failed to generate VAPID keys: %v", err)
	}

	// Save to .env format
	envContent := fmt.Sprintf(`# Web Push VAPID Keys
# Add these to your .env file or export them as environment variables

VAPID_PUBLIC_KEY=%s
VAPID_PRIVATE_KEY=%s
VAPID_SUBJECT=mailto:your-email@example.com
`,
		publicKey,
		privateKey,
	)

	// Save to file
	envFile := "./data/vapid_keys.env"
	if err := os.WriteFile(envFile, []byte(envContent), 0600); err != nil {
		log.Fatalf("Failed to write keys to file: %v", err)
	}

	fmt.Println("✅ VAPID keys generated successfully!")
	fmt.Println()
	fmt.Println("Keys saved to:", envFile)
	fmt.Println()
	fmt.Println("Add these to your .env file:")
	fmt.Println("----------------------------------------")
	fmt.Println(envContent)
	fmt.Println("----------------------------------------")
	fmt.Println()
	fmt.Printf("Public Key (for frontend): %s\n", publicKey)
	fmt.Println()

	// Also output as base64 URL encoding check
	fmt.Println("Verification:")
	fmt.Printf("Public key length: %d chars\n", len(publicKey))
	fmt.Printf("Private key length: %d chars\n", len(privateKey))

	// Decode to verify it's valid base64
	_, err = base64.RawURLEncoding.DecodeString(publicKey)
	if err != nil {
		fmt.Printf("⚠️  Warning: Public key may not be valid base64: %v\n", err)
	} else {
		fmt.Println("✅ Public key is valid base64 URL encoding")
	}
}
