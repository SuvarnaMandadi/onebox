-- Chat attachments: files uploaded into the admin AI copilot chat (images
-- for vision-capable providers, documents made available as chat context —
-- see the "Add ChatGPT-style multimodal chat" feature). Storage reuses the
-- existing _files table (kind='chat_attachment', the same pattern already
-- used to keep avatar uploads out of the Files browser — see
-- 0009_display_name_and_file_kind.sql). This table holds the chat-specific
-- metadata that doesn't belong on a generic file record: whether it's an
-- "image" or "document" attachment, and — for documents — the text already
-- extracted from it at upload time, so referencing an attachment in chat
-- never has to re-parse a PDF/DOCX/XLSX on every turn just to reuse it.
CREATE TABLE _chat_attachments (
    id             TEXT PRIMARY KEY REFERENCES _files (id) ON DELETE CASCADE,
    owner_id       TEXT,
    kind           TEXT NOT NULL, -- "image" or "document"
    extracted_text TEXT NOT NULL DEFAULT '',
    created        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX idx_chat_attachments_owner_id ON _chat_attachments (owner_id);
