CREATE TABLE `adapters` (
	`mac` text PRIMARY KEY NOT NULL,
	`name` text,
	`reported_name` text,
	`role` text DEFAULT 'unknown' NOT NULL,
	`last_known_ip` text,
	`last_known_channel` integer,
	`product_name` text,
	`model` text,
	`firmware` text,
	`first_seen_at` integer NOT NULL,
	`last_seen_at` integer
);
