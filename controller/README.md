Controller for persistent GStreamer playout with runtime source switching

This controller builds one GStreamer pipeline and switches sources (PNG, lesson loop, playlist) without restarting gst. Audio outputs are fully configurable to cover all Dante streams (35+).

Files
- controller/lesson-controller.go: Go controller (to be implemented) exposing HTTP API
- controller/audio_map.json.sample: Example mapping of outputs → JACK ports and language/channel selection
- controller/playlist.json.sample: Example playlist format for pre-generated items
- bin/run_lesson_controller.sh: Build-and-run helper script

HTTP API (planned)
- POST /source/png
- POST /source/lesson { path, loop, map? }
- POST /playlist/load { file }
- POST /playlist/start
- POST /playlist/next

Audio mapping
Define all outputs (e.g., 35) in audio_map.json. Each output becomes an input-selector + jack sink.

Build & run
- First time: the script will build the module in controller/ and run it
- ./bin/run_lesson_controller.sh


