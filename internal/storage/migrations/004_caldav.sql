-- Интеграция с Apple Календарём (iCloud CalDAV) и привязка событий.
ALTER TABLE users ADD COLUMN caldav_login    TEXT;
ALTER TABLE users ADD COLUMN caldav_password TEXT;
ALTER TABLE events ADD COLUMN caldav_uid     TEXT;
