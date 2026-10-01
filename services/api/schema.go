package main

const schema = `
CREATE TABLE IF NOT EXISTS users (
 id TEXT PRIMARY KEY, email TEXT NOT NULL UNIQUE COLLATE NOCASE, name TEXT NOT NULL,
 password TEXT NOT NULL, role TEXT NOT NULL DEFAULT 'reader' CHECK(role IN ('reader','admin')),
 bio TEXT NOT NULL DEFAULT '', avatar TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL,
 totp_secret TEXT NOT NULL DEFAULT '', totp_pending TEXT NOT NULL DEFAULT '', totp_pending_until INTEGER NOT NULL DEFAULT 0,
 totp_last INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS sessions (hash TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, expires INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS remembered_accounts (hash TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, expires INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS remembered_accounts_user ON remembered_accounts(user_id, expires);
CREATE TABLE IF NOT EXISTS email_codes (email TEXT PRIMARY KEY COLLATE NOCASE, hash TEXT NOT NULL, expires INTEGER NOT NULL, attempts INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS email_changes (user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, email TEXT NOT NULL COLLATE NOCASE, hash TEXT NOT NULL, expires INTEGER NOT NULL, attempts INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS recovery_codes (user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, hash TEXT NOT NULL, PRIMARY KEY(user_id, hash));
CREATE TABLE IF NOT EXISTS posts (
 id TEXT PRIMARY KEY, slug TEXT NOT NULL UNIQUE, title TEXT NOT NULL, description TEXT NOT NULL DEFAULT '',
 markdown TEXT NOT NULL, cover TEXT NOT NULL DEFAULT '', category TEXT NOT NULL DEFAULT '随笔', tags TEXT NOT NULL DEFAULT '[]',
 date TEXT NOT NULL, status TEXT NOT NULL CHECK(status IN ('draft','published','deleted')),
 revision INTEGER NOT NULL DEFAULT 1, updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS notices (id TEXT PRIMARY KEY, title TEXT NOT NULL, text TEXT NOT NULL, status TEXT NOT NULL CHECK(status IN ('draft','published')), updated_at TEXT NOT NULL, revision INTEGER NOT NULL DEFAULT 1);
CREATE TABLE IF NOT EXISTS comments (id TEXT PRIMARY KEY, post_id TEXT NOT NULL REFERENCES posts(id), user_id TEXT NOT NULL REFERENCES users(id), text TEXT NOT NULL, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS bookmarks (user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, post_id TEXT NOT NULL REFERENCES posts(id) ON DELETE CASCADE, PRIMARY KEY(user_id, post_id));
CREATE TABLE IF NOT EXISTS likes (user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, post_id TEXT NOT NULL REFERENCES posts(id) ON DELETE CASCADE, PRIMARY KEY(user_id, post_id));
CREATE TABLE IF NOT EXISTS post_views (post_id TEXT NOT NULL REFERENCES posts(id) ON DELETE CASCADE, visitor TEXT NOT NULL, day TEXT NOT NULL, PRIMARY KEY(post_id, visitor, day));
CREATE TABLE IF NOT EXISTS settings (name TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS post_aliases (path TEXT PRIMARY KEY, post_id TEXT NOT NULL REFERENCES posts(id) ON DELETE CASCADE);
CREATE TABLE IF NOT EXISTS indexnow_queue (path TEXT PRIMARY KEY, version INTEGER NOT NULL DEFAULT 1, next_at INTEGER NOT NULL DEFAULT 0, attempts INTEGER NOT NULL DEFAULT 0);
CREATE TRIGGER IF NOT EXISTS indexnow_post_insert AFTER INSERT ON posts WHEN NEW.status='published' BEGIN
 INSERT INTO indexnow_queue(path) VALUES('/posts/'||NEW.slug||'/') ON CONFLICT(path) DO UPDATE SET version=version+1,next_at=0,attempts=0;
END;
CREATE TRIGGER IF NOT EXISTS indexnow_post_update AFTER UPDATE ON posts WHEN OLD.status='published' OR NEW.status='published' BEGIN
 INSERT INTO indexnow_queue(path) VALUES('/posts/'||NEW.slug||'/') ON CONFLICT(path) DO UPDATE SET version=version+1,next_at=0,attempts=0;
END;
CREATE TRIGGER IF NOT EXISTS indexnow_post_delete AFTER DELETE ON posts WHEN OLD.status='published' BEGIN
 INSERT INTO indexnow_queue(path) VALUES('/posts/'||OLD.slug||'/') ON CONFLICT(path) DO UPDATE SET version=version+1,next_at=0,attempts=0;
END;
CREATE TRIGGER IF NOT EXISTS indexnow_notice_insert AFTER INSERT ON notices WHEN NEW.status='published' BEGIN
 INSERT INTO indexnow_queue(path) VALUES('/notices/'||NEW.id||'/') ON CONFLICT(path) DO UPDATE SET version=version+1,next_at=0,attempts=0;
END;
CREATE TRIGGER IF NOT EXISTS indexnow_notice_update AFTER UPDATE ON notices WHEN OLD.status='published' OR NEW.status='published' BEGIN
 INSERT INTO indexnow_queue(path) VALUES('/notices/'||NEW.id||'/') ON CONFLICT(path) DO UPDATE SET version=version+1,next_at=0,attempts=0;
END;
CREATE TRIGGER IF NOT EXISTS indexnow_notice_delete AFTER DELETE ON notices WHEN OLD.status='published' BEGIN
 INSERT INTO indexnow_queue(path) VALUES('/notices/'||OLD.id||'/') ON CONFLICT(path) DO UPDATE SET version=version+1,next_at=0,attempts=0;
END;
CREATE TABLE IF NOT EXISTS media (id TEXT PRIMARY KEY, path TEXT NOT NULL, config TEXT NOT NULL, mime TEXT NOT NULL, size INTEGER NOT NULL, owner_id TEXT NOT NULL REFERENCES users(id), created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS media_cleanup (id TEXT PRIMARY KEY, path TEXT NOT NULL, config TEXT NOT NULL, next_at INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS post_events (seq INTEGER PRIMARY KEY AUTOINCREMENT, post_id TEXT NOT NULL REFERENCES posts(id) ON DELETE CASCADE, revision INTEGER NOT NULL, UNIQUE(post_id, revision));
CREATE TABLE IF NOT EXISTS subscriptions (
 user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 enabled INTEGER NOT NULL DEFAULT 0, frequency TEXT NOT NULL DEFAULT 'daily' CHECK(frequency IN ('immediate','daily','weekly')),
 cursor INTEGER NOT NULL DEFAULT 0, next_at INTEGER NOT NULL DEFAULT 0, last_sent INTEGER NOT NULL DEFAULT 0,
 last_error TEXT NOT NULL DEFAULT '', revision INTEGER NOT NULL DEFAULT 1
);
CREATE TRIGGER IF NOT EXISTS post_insert_event AFTER INSERT ON posts WHEN NEW.status='published' BEGIN
 INSERT OR IGNORE INTO post_events(post_id,revision) VALUES(NEW.id,NEW.revision);
END;
CREATE TRIGGER IF NOT EXISTS post_update_event AFTER UPDATE ON posts
 WHEN NEW.status='published' AND (OLD.status<>'published' OR OLD.title<>NEW.title OR OLD.description<>NEW.description OR OLD.markdown<>NEW.markdown OR OLD.cover<>NEW.cover OR OLD.category<>NEW.category OR OLD.tags<>NEW.tags OR OLD.date<>NEW.date) BEGIN
 INSERT OR IGNORE INTO post_events(post_id,revision) VALUES(NEW.id,NEW.revision);
END;
CREATE TABLE IF NOT EXISTS audit (id INTEGER PRIMARY KEY, user_id TEXT NOT NULL, action TEXT NOT NULL, target TEXT NOT NULL, created_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS comments_post ON comments(post_id, created_at);
CREATE INDEX IF NOT EXISTS posts_status ON posts(status, date);
CREATE INDEX IF NOT EXISTS likes_post ON likes(post_id);
CREATE INDEX IF NOT EXISTS bookmarks_post ON bookmarks(post_id);
CREATE INDEX IF NOT EXISTS subscriptions_due ON subscriptions(enabled, next_at);
PRAGMA user_version=5;
`
