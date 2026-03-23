-- Phase 1: Inventory enhancements - MAC flags + channel
ALTER TABLE inventory_devices ADD COLUMN IF NOT EXISTS channel INTEGER;
ALTER TABLE inventory_devices ADD COLUMN IF NOT EXISTS locally_administered BOOLEAN DEFAULT false;
ALTER TABLE inventory_devices ADD COLUMN IF NOT EXISTS multicast BOOLEAN DEFAULT false;
