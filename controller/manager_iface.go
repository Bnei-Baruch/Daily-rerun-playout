package main

type PipelineManager interface {
	BuildPipeline() error
	SwitchToPNG() error
	SwitchToLesson(path string, loop bool, trackMap map[string]int) error
	LoadPlaylist(p Playlist) error
	StartPlaylist() error
	NextItem() error
	Stop() error
}
