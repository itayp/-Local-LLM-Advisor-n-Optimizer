# Sampler fixtures

Each file is one reading of one tool, in the tool's own output format, as
the sampler's probes parse it. None was captured from a fleet machine: the
formats follow each tool's documentation and the fleet's step-0 runs
(scripts/probe0 read nvidia-smi's memory.used, vm_stat's "Pages wired down",
ioreg's IOAccelerator statistics and amdgpu's sysfs counters on the three
machines); the values are chosen to match those machines. Replace a file
with a captured one when one is at hand, and say so here.

- `nvidia-smi.txt` — `nvidia-smi --query-gpu=index,utilization.gpu,memory.used,temperature.gpu,power.draw --format=csv,noheader,nounits`
  on a desktop with two cards; the second is a laptop-style part whose
  power reading is "[N/A]".
- `vm_stat.txt` — `vm_stat` on an Apple Silicon Mac (16 KiB pages).
- `ioreg.txt` — `ioreg -r -d 1 -w 0 -c IOAccelerator` on an M1 Pro,
  trimmed to the AGXAccelerator entry's PerformanceStatistics.
- `meminfo.txt` — `/proc/meminfo` on a 64 GB Linux desktop.
