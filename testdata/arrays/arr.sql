-- package: arrays

-- model: Group { id int64, name string }
-- model: ServicesTable { id int64, table_name string, operations []string }

-- param: group_ids []int64
-- name: FindGroupNamesByIDs :many
-- model { id int64, name string }
SELECT id, name FROM groups WHERE id = ANY(@group_ids)

-- param: table_names []string
-- name: FindMetadataByNames :many
-- model { system_id int64, table_name string }
SELECT system_id, table_name FROM metadata WHERE table_name = ANY(@table_names)

-- param: table_name string, operations []string
-- name: InsertServicesTable :one
-- model int64
INSERT INTO services_table (table_name, operations) VALUES (@table_name, @operations) RETURNING id

-- param: id int64
-- name: FindServicesTableByID :one
-- model: ServicesTable
SELECT id, table_name, operations FROM services_table WHERE id = @id
