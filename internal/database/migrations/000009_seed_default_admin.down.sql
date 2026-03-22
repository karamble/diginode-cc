-- Remove the seeded default admin (only if it's the original seeded account)
DELETE FROM users WHERE email = 'admin@example.com' AND name = 'Admin';
