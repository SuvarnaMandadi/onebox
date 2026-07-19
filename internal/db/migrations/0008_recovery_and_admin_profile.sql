-- Mirrors _users' profile columns onto _admins (so an admin account gets
-- the same Account-page profile UI as a regular user), and adds a
-- recovery-phrase hash to both — the self-service "forgot password"
-- mechanism from a 12-word recovery phrase, verified the same way a
-- password is (see internal/auth/recovery.go).
ALTER TABLE _admins ADD COLUMN first_name TEXT NOT NULL DEFAULT '';
ALTER TABLE _admins ADD COLUMN last_name TEXT NOT NULL DEFAULT '';
ALTER TABLE _admins ADD COLUMN phone TEXT NOT NULL DEFAULT '';
ALTER TABLE _admins ADD COLUMN avatar_file_id TEXT;
ALTER TABLE _admins ADD COLUMN recovery_phrase_hash TEXT;

ALTER TABLE _users ADD COLUMN recovery_phrase_hash TEXT;

-- Generalize the admin-assisted reset-token table to also cover admin
-- accounts recovering (or being handed) their own credentials, and
-- promotion (see POST /api/admins/promote): subject_type + subject_id
-- together identify which table/row the token applies to.
ALTER TABLE _password_resets ADD COLUMN subject_type TEXT NOT NULL DEFAULT 'user';
