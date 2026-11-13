//go:build !linux && !darwin

package server

import "context"

// processCwd is unknown here: the client falls back to the checkout root.
func processCwd(context.Context, int) string { return "" }
