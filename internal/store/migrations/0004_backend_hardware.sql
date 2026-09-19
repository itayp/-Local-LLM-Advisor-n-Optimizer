-- Step 5 (fit estimator + recommendation engine).
--
-- Which hardware a backend check ran on. The runtime path a check records
-- (runtime_paths_json) is only known while a model is loaded, so most checks
-- record none, and the recommendation engine plans against the last check
-- that did — "the runtime was SEEN running on the processor" outlives the
-- model being unloaded. It must not outlive the hardware: what Ollama did
-- with the old graphics card says nothing about the new one. The fingerprint
-- is hardware.Fingerprint, the same value hardware_profiles.fingerprint
-- holds; '' for rows written before this migration, or before detection had
-- finished, which therefore count for no hardware.
ALTER TABLE backends ADD COLUMN hardware_fingerprint TEXT NOT NULL DEFAULT '';
CREATE INDEX backends_observed ON backends (name, hardware_fingerprint, id);
