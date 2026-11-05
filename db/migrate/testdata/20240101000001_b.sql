-- +goose Up
CREATE TABLE b (id INT PRIMARY KEY, a_id INT NOT NULL REFERENCES a (id), name TEXT UNIQUE);
-- +goose Down
DROP TABLE b;
