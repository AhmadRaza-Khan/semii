-- +goose Up
CREATE TABLE a (id INT PRIMARY KEY);
-- +goose Down
DROP TABLE a;
