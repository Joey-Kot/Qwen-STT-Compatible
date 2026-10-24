module qwen-stt-compatible

go 1.27.3

require (
	github.com/Joey-Kot/ASR-Audio-Preprocess v0.0.0-20261024024023-ca5657ffb833
	// Includes the upstream crypto/rand mask-key fix missing from v1.5.3.
	github.com/gorilla/websocket v1.5.4-0.20240701034025-d67f41855da4
)

require golang.org/x/net v0.61.0 // indirect
