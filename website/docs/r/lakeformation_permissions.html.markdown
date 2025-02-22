---
subcategory: "Lake Formation"
layout: "aws"
page_title: "AWS: aws_lakeformation_permissions"
description: |-
    Grants permissions to the principal to access metadata in the Data Catalog and data organized in underlying data storage such as Amazon S3.
---

# Resource: aws_lakeformation_permissions

Grants permissions to the principal to access metadata in the Data Catalog and data organized in underlying data storage such as Amazon S3. Permissions are granted to a principal, in a Data Catalog, relative to a Lake Formation resource, which includes the Data Catalog, databases, and tables. For more information, see [Security and Access Control to Metadata and Data in Lake Formation](https://docs.aws.amazon.com/lake-formation/latest/dg/security-data-access.html).

~> **NOTE:** Lake Formation grants implicit permissions to data lake administrators, database creators, and table creators. These implicit permissions cannot be revoked _per se_. If this resource reads implicit permissions, it will attempt to revoke them, which causes an error when the resource is destroyed. There are two ways to avoid these errors. First, grant explicit permissions (and `permissions_with_grant_option`) to "overwrite" a principal's implicit permissions, which you can then revoke with this resource. Second, avoid using this resource with principals that have implicit permissions. For more information, see [Implicit Lake Formation Permissions](https://docs.aws.amazon.com/lake-formation/latest/dg/implicit-permissions.html).

## Example Usage

### Grant Permissions For A Lake Formation S3 Resource

```terraform
resource "aws_lakeformation_permissions" "test" {
  principal   = aws_iam_role.workflow_role.arn
  permissions = ["ALL"]

  data_location {
    arn = aws_lakeformation_resource.test.arn
  }
}
```

### Grant Permissions For A Glue Catalog Database

```terraform
resource "aws_lakeformation_permissions" "test" {
  role        = aws_iam_role.workflow_role.arn
  permissions = ["CREATE_TABLE", "ALTER", "DROP"]

  database {
    name       = aws_glue_catalog_database.test.name
    catalog_id = "110376042874"
  }
}
```

## Argument Reference

The following arguments are required:

* `permissions` – (Required) List of permissions granted to the principal. Valid values may include `ALL`, `ALTER`, `CREATE_DATABASE`, `CREATE_TABLE`, `DATA_LOCATION_ACCESS`, `DELETE`, `DESCRIBE`, `DROP`, `INSERT`, and `SELECT`. For details on each permission, see [Lake Formation Permissions Reference](https://docs.aws.amazon.com/lake-formation/latest/dg/lf-permissions-reference.html).
* `principal` – (Required) Principal to be granted the permissions on the resource. Supported principals include IAM roles, users, groups, SAML groups and users, QuickSight groups, OUs, and organizations as well as AWS account IDs for cross-account permissions. For more information, see [Lake Formation Permissions Reference](https://docs.aws.amazon.com/lake-formation/latest/dg/lf-permissions-reference.html).

~> **NOTE:** If the `principal` is also a data lake administrator, AWS grants implicit permissions that can cause errors using this resource. For example, AWS implicitly grants a `principal`/administrator `permissions` and `permissions_with_grant_option` of `ALL`, `ALTER`, `DELETE`, `DESCRIBE`, `DROP`, `INSERT`, and `SELECT` on a table. If you use this resource to explicitly grant the `principal`/administrator `permissions` but _not_ `permissions_with_grant_option` of `ALL`, `ALTER`, `DELETE`, `DESCRIBE`, `DROP`, `INSERT`, and `SELECT` on the table, this resource will read the implicit `permissions_with_grant_option` and attempt to revoke them when the resource is destroyed. Doing so will cause an `InvalidInputException: No permissions revoked` error because you cannot revoke implicit permissions _per se_. To workaround this problem, explicitly grant the `principal`/administrator `permissions` _and_ `permissions_with_grant_option`, which can then be revoked. Similarly, granting a `principal`/administrator permissions on a table with columns and providing `column_names`, will result in a `InvalidInputException: Permissions modification is invalid` error because you are narrowing the implicit permissions. Instead, set `wildcard` to `true` and remove the `column_names`.

One of the following is required:

* `catalog_resource` - (Optional) Whether the permissions are to be granted for the Data Catalog. Defaults to `false`.
* `data_location` - (Optional) Configuration block for a data location resource. Detailed below.
* `database` - (Optional) Configuration block for a database resource. Detailed below.
* `table` - (Optional) Configuration block for a table resource. Detailed below.
* `table_with_columns` - (Optional) Configuration block for a table with columns resource. Detailed below.

The following arguments are optional:

* `catalog_id` – (Optional) Identifier for the Data Catalog. By default, the account ID. The Data Catalog is the persistent metadata store. It contains database definitions, table definitions, and other control information to manage your Lake Formation environment.
* `permissions_with_grant_option` - (Optional) Subset of `permissions` which the principal can pass.

### data_location

The following argument is required:

* `arn` – (Required) Amazon Resource Name (ARN) that uniquely identifies the data location resource.

The following argument is optional:

* `catalog_id` - (Optional) Identifier for the Data Catalog where the location is registered with Lake Formation. By default, it is the account ID of the caller.

### database

The following argument is required:

* `name` – (Required) Name of the database resource. Unique to the Data Catalog.

The following argument is optional:

* `catalog_id` - (Optional) Identifier for the Data Catalog. By default, it is the account ID of the caller.

### table

The following argument is required:

* `database_name` – (Required) Name of the database for the table. Unique to a Data Catalog.
* `name` - (Required, at least one of `name` or `wildcard`) Name of the table.
* `wildcard` - (Required, at least one of `name` or `wildcard`) Whether to use a wildcard representing every table under a database. Defaults to `false`.

The following arguments are optional:

* `catalog_id` - (Optional) Identifier for the Data Catalog. By default, it is the account ID of the caller.

### table_with_columns

The following arguments are required:

* `column_names` - (Required, at least one of `column_names` or `wildcard`) Set of column names for the table.
* `database_name` – (Required) Name of the database for the table with columns resource. Unique to the Data Catalog.
* `name` – (Required) Name of the table resource.
* `wildcard` - (Required, at least one of `column_names` or `wildcard`) Whether to use a column wildcard. If `excluded_column_names` is included, `wildcard` must be set to `true` to avoid Terraform reporting a difference.

The following arguments are optional:

* `catalog_id` - (Optional) Identifier for the Data Catalog. By default, it is the account ID of the caller.
* `excluded_column_names` - (Optional) Set of column names for the table to exclude. If `excluded_column_names` is included, `wildcard` must be set to `true` to avoid Terraform reporting a difference.

## Attributes Reference

No additional attributes are exported.
