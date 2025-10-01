//go:build !gogst

package main

import "fmt"

// tryNewGoGst returns an error under non-gogst builds
func tryNewGoGst(m AudioMap) (PipelineManager, error) {
	return nil, fmt.Errorf("go-gst manager not built (no gogst tag)")
}
