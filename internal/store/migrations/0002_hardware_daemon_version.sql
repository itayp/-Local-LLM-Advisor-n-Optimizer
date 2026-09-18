-- Step 2: which build of the daemon wrote each hardware profile.
--
-- Detection improves between releases. A fingerprint that changes together
-- with daemon_version may be the same hardware read better rather than new
-- hardware, and the UI can say so instead of announcing a new machine.
-- Rows written before this migration have '' (unknown).
ALTER TABLE hardware_profiles ADD COLUMN daemon_version TEXT NOT NULL DEFAULT '';
