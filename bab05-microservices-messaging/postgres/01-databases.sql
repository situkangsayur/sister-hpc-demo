-- Satu instance Postgres, satu database per service (hemat memori untuk lab).
-- Di produksi: instance atau minimal user terpisah per service agar batas data ditegakkan.
CREATE DATABASE catalog;
CREATE DATABASE orders;
CREATE DATABASE inventory;
CREATE DATABASE payment;
CREATE DATABASE notification;
