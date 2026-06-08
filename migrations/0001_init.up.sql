-- Links: one row per shortened URL.
CREATE TABLE IF NOT EXISTS links (
    id          BIGINT PRIMARY KEY,            -- base62-encoded into the short code
    code        TEXT        NOT NULL,
    long_url    TEXT        NOT NULL,
    url_hash    BYTEA       NOT NULL,           -- sha256(normalized url), for dedupe
    custom      BOOLEAN     NOT NULL DEFAULT FALSE,
    click_count BIGINT      NOT NULL DEFAULT 0, -- maintained by the analytics workers
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ
);

-- The short code is the public identifier and must be globally unique.
CREATE UNIQUE INDEX IF NOT EXISTS links_code_key ON links (code);

-- Dedupe: auto-generated, non-expiring links for the same URL collapse to one row.
-- Custom aliases and TTL links are intentionally exempt (partial index predicate).
CREATE UNIQUE INDEX IF NOT EXISTS links_url_hash_dedupe
    ON links (url_hash)
    WHERE custom = FALSE AND expires_at IS NULL;

-- Supports a future TTL sweep / "expiring soon" queries.
CREATE INDEX IF NOT EXISTS links_expires_at_idx ON links (expires_at)
    WHERE expires_at IS NOT NULL;

-- Sequence that feeds link ids. Starting high so the very first short codes are a
-- pleasant length (base62(1_000_000) = "4c92" ... we start higher for ~6 chars).
CREATE SEQUENCE IF NOT EXISTS links_id_seq START WITH 1000000000 INCREMENT BY 1;

-- Clicks: append-only analytics, written in batches off the redirect hot path.
CREATE TABLE IF NOT EXISTS clicks (
    id         BIGSERIAL PRIMARY KEY,
    link_code  TEXT        NOT NULL REFERENCES links (code) ON DELETE CASCADE,
    referrer   TEXT        NOT NULL DEFAULT '',
    user_agent TEXT        NOT NULL DEFAULT '',
    ip         TEXT        NOT NULL DEFAULT '',
    clicked_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS clicks_link_code_idx ON clicks (link_code);
CREATE INDEX IF NOT EXISTS clicks_clicked_at_idx ON clicks (clicked_at);
