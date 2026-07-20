package main

import (
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

func main() {
	serverURL := flag.String("url", os.Getenv("USAGEWIDGET_PUBLIC_URL"), "private HTTPS UsageWidget server URL")
	token := flag.String("token", os.Getenv("USAGEWIDGET_TOKEN"), "UsageWidget bearer token")
	flag.Parse()
	if !strings.HasPrefix(*serverURL, "https://") || len(*token) < 32 {
		fmt.Fprintln(os.Stderr, "usagewidget: a private HTTPS URL and bearer token of at least 32 characters are required")
		os.Exit(2)
	}
	payload := "usagewidget://configure?v=1&server=" + url.QueryEscape(strings.TrimSuffix(*serverURL, "/")) + "&token=" + url.QueryEscape(*token)
	qr, err := qrcode.New(payload, qrcode.Medium)
	if err != nil {
		fmt.Fprintf(os.Stderr, "usagewidget: create QR: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "usagewidget: This QR grants full single-operator access to the server. Keep it private.")
	bitmap := qr.Bitmap()
	quiet := 2
	for y := -quiet; y < len(bitmap)+quiet; y += 2 {
		for x := -quiet; x < len(bitmap)+quiet; x++ {
			top := y >= 0 && y < len(bitmap) && x >= 0 && x < len(bitmap) && bitmap[y][x]
			bottom := y+1 >= 0 && y+1 < len(bitmap) && x >= 0 && x < len(bitmap) && bitmap[y+1][x]
			switch {
			case top && bottom:
				fmt.Print("█")
			case top:
				fmt.Print("▀")
			case bottom:
				fmt.Print("▄")
			default:
				fmt.Print(" ")
			}
		}
		fmt.Println()
	}
}
