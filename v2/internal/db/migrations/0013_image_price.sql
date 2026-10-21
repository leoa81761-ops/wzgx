-- price per generated image (yuan) for image configs, so spend caps count image spend
ALTER TABLE service_config ADD COLUMN price_per_image NUMERIC(12,4) CHECK (price_per_image IS NULL OR price_per_image >= 0);
