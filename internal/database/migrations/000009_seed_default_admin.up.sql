-- Seed default admin user (admin@example.com / admin) if no users exist.
-- This runs once on fresh installations to ensure the system is accessible.
INSERT INTO users (email, password_hash, name, role)
SELECT 'admin@example.com',
       '$2a$10$pdc.F5coo6FIwTvkD4IBUODFYY9/7QSXUcZWPvn9DKz8gTGS.OZ6q',
       'Admin',
       'ADMIN'
WHERE NOT EXISTS (SELECT 1 FROM users LIMIT 1);
