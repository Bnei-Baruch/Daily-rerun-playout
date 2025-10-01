//go:build gogst

package main

func tryNewGoGst(m AudioMap) (PipelineManager, error) {
	return NewGoGstManager(m), nil
}
