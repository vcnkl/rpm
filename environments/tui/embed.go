package tui

import "embed"

//go:embed bundle/*
var bundleFS embed.FS

func BundlePath() string {
	return "bundle/index.js"
}
