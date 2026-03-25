ALTER TABLE alert_rules DROP COLUMN IF EXISTS notify_email;
ALTER TABLE alert_rules DROP COLUMN IF EXISTS email_recipients;
ALTER TABLE alert_rules DROP COLUMN IF EXISTS notify_visual;
ALTER TABLE alert_rules DROP COLUMN IF EXISTS notify_audible;
