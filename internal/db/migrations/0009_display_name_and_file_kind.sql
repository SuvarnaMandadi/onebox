ALTER TABLE _users ADD COLUMN display_name TEXT NOT NULL DEFAULT '';
ALTER TABLE _admins ADD COLUMN display_name TEXT NOT NULL DEFAULT '';

-- kind distinguishes avatar uploads from regular file uploads so avatars
-- never show up in the Files (File Storage) listing/UI, while still being
-- servable at GET /api/files/:id (the dashboard fetches avatars from
-- there as an authenticated blob).
ALTER TABLE _files ADD COLUMN kind TEXT NOT NULL DEFAULT 'file';
