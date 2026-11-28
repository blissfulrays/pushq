-- An application's icon: a small image as a data: URL (the console resizes
-- uploads to 128px). NULL = show the name's initials.
ALTER TABLE apps ADD COLUMN icon TEXT;
