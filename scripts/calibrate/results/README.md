# Calibration results

One JSON file per machine and day, written by `go run ./scripts/calibrate`
(see ../README.md). Commit them: they are the fleet's evidence for the speed
ranges in `internal/estimate/config.go`, the way `scripts/probe0/results/`
is for the memory formula.
