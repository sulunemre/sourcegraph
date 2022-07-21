CREATE TABLE IF NOT EXISTS service_registry
(
    ip                text,
    port              integer NOT NULL,
    service           text    NOT NULL,
    health_check_path text    NOT NULL,
    last_heartbeat    timestamp with time zone DEFAULT now(),
    PRIMARY KEY (ip, port)
);

COMMENT ON TABLE service_registry IS 'Records services that register with the service registry in frontend.';

COMMENT ON COLUMN service_registry.last_heartbeat IS 'The last time the service sent a renewal request to the service registry.';

COMMENT ON COLUMN service_registry.health_check_path IS '(Optional) A path such that ip:port/health_check_path serves a health check';
