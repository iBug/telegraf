# SQL Output Plugin

This plugin writes metrics to a supported SQL database using a simple,
hard-coded database schema. There is a table for each metric type with the
table name corresponding to the metric name. There is a column per field
and a column per tag with an optional column for the metric timestamp.

A row is written for every metric. This means multiple metrics are never
merged into a single row, even if they have the same metric name, tags, and
timestamp.

The plugin uses Golang's generic "database/sql" interface and third party
drivers. See the driver-specific section for a list of supported drivers
and details.

⭐ Telegraf v1.19.0
🏷️ datastore
💻 all

## Getting started

To use the plugin, set the driver setting to the driver name appropriate for
your database. Then set the data source name (DSN). The format of the DSN varies
by driver but often includes a username, password, the database instance to use,
and the hostname of the database server. The user account must have privileges
to insert rows and create tables.

## Generated SQL

The plugin generates simple ANSI/ISO SQL that is likely to work on any DBMS. It
doesn't use language features that are specific to a particular DBMS. If you
want to use a feature that is specific to a particular DBMS, you may be able to
set it up manually outside of this plugin or through the init_sql setting.

The insert statements generated by the plugin use placeholder parameters. Most
database drivers use question marks as placeholders but postgres uses indexed
dollar signs. The plugin chooses which placeholder style to use depending on the
driver selected.

Through the nature of the inputs plugins, the amounts of columns inserted within
rows for a given metric may differ. Since the tables are created based on the
tags and fields available within an input metric, it's possible the created
table won't contain all the necessary columns. If you wish to automate table
updates, check out the [Schema updates](#schema-updates) section for more info.

## Advanced options

When the plugin first connects it runs SQL from the init_sql setting, allowing
you to perform custom initialization for the connection.

Before inserting a row, the plugin checks whether the table exists. If it
doesn't exist, the plugin creates the table. The existence check and the table
creation statements can be changed through template settings. The template
settings allows you to have the plugin create customized tables or skip table
creation entirely by setting the check template to any query that executes
without error, such as "select 1".

The name of the timestamp column is "timestamp" but it can be changed with the
timestamp\_column setting. The timestamp column can be completely disabled by
setting it to "".

By changing the table creation template, it's possible with some databases to
save a row insertion timestamp. You can add an additional column with a default
value to the template, like "CREATE TABLE {TABLE}(insertion_timestamp TIMESTAMP
DEFAULT CURRENT\_TIMESTAMP, {COLUMNS})".

The mapping of metric types to sql column types can be customized through the
convert settings.

If your database server supports Prepared Statements / Batch / Bulk Inserts,
you could improve the ingestion rate, by enabling `batch_transactions`

## Global configuration options <!-- @/docs/includes/plugin_config.md -->

In addition to the plugin-specific configuration settings, plugins support
additional global and plugin configuration settings. These settings are used to
modify metrics, tags, and field or create aliases and configure ordering, etc.
See the [CONFIGURATION.md][CONFIGURATION.md] for more details.

[CONFIGURATION.md]: ../../../docs/CONFIGURATION.md#plugins

## Secret-store support

This plugin supports secrets from secret-stores for the `data_source_name`
option. See the [secret-store documentation][SECRETSTORE] for more details on
how to use them.

[SECRETSTORE]: ../../../docs/CONFIGURATION.md#secret-store-secrets

## Configuration

```toml @sample.conf
# Save metrics to an SQL Database
[[outputs.sql]]
  ## Database driver
  ## Valid options: mssql (Microsoft SQL Server), mysql (MySQL), pgx (Postgres),
  ##  sqlite (SQLite3), snowflake (snowflake.com) clickhouse (ClickHouse)
  driver = ""

  ## Data source name
  ## The format of the data source name is different for each database driver.
  ## See the plugin readme for details.
  data_source_name = ""

  ## Timestamp column name, set to empty to ignore the timestamp
  # timestamp_column = "timestamp"

  ## Table creation template
  ## Available template variables:
  ##  {TABLE} - table name as a quoted identifier
  ##  {TABLELITERAL} - table name as a quoted string literal
  ##  {COLUMNS} - column definitions (list of quoted identifiers and types)
  ##  {TAG_COLUMN_NAMES} - tag column definitions (list of quoted identifiers)
  ##  {TIMESTAMP_COLUMN_NAME} - the name of the time stamp column, as configured above
  # table_template = "CREATE TABLE {TABLE}({COLUMNS})"
  ## NOTE: For the clickhouse driver the default is:
  # table_template = "CREATE TABLE {TABLE}({COLUMNS}) ORDER BY ({TAG_COLUMN_NAMES}, {TIMESTAMP_COLUMN_NAME})"

  ## Table existence check template
  ## Available template variables:
  ##  {TABLE} - tablename as a quoted identifier
  # table_exists_template = "SELECT 1 FROM {TABLE} LIMIT 1"

  ## Table update template, available template variables:
  ##  {TABLE} - table name as a quoted identifier
  ##  {COLUMN} - column definition (quoted identifier and type)
  ## NOTE: Ensure the user (you're using to write to the database) has necessary permissions
  ##
  ## Use the following setting for automatically adding columns:
  ## table_update_template = "ALTER TABLE {TABLE} ADD COLUMN {COLUMN}"
  # table_update_template = ""

  ## Initialization SQL
  # init_sql = ""

  ## Send metrics with the same columns and the same table as batches using prepared statements
  # batch_transactions = false

  ## Maximum amount of time a connection may be idle. "0s" means connections are
  ## never closed due to idle time.
  # connection_max_idle_time = "0s"

  ## Maximum amount of time a connection may be reused. "0s" means connections
  ## are never closed due to age.
  # connection_max_lifetime = "0s"

  ## Maximum number of connections in the idle connection pool. 0 means unlimited.
  # connection_max_idle = 2

  ## Maximum number of open connections to the database. 0 means unlimited.
  # connection_max_open = 0

  ## NOTE: Due to the way TOML is parsed, tables must be at the END of the
  ## plugin definition, otherwise additional config options are read as part of
  ## the table

  ## Metric type to SQL type conversion
  ## The values on the left are the data types Telegraf has and the values on
  ## the right are the data types Telegraf will use when sending to a database.
  ##
  ## The database values used must be data types the destination database
  ## understands. It is up to the user to ensure that the selected data type is
  ## available in the database they are using. Refer to your database
  ## documentation for what data types are available and supported.
  #[outputs.sql.convert]
  #  integer              = "INT"
  #  real                 = "DOUBLE"
  #  text                 = "TEXT"
  #  timestamp            = "TIMESTAMP"
  #  defaultvalue         = "TEXT"
  #  unsigned             = "UNSIGNED"
  #  bool                 = "BOOL"
  #  ## This setting controls the behavior of the unsigned value. By default the
  #  ## setting will take the integer value and append the unsigned value to it. The other
  #  ## option is "literal", which will use the actual value the user provides to
  #  ## the unsigned option. This is useful for a database like ClickHouse where
  #  ## the unsigned value should use a value like "uint64".
  #  # conversion_style = "unsigned_suffix"
```

## Schema updates

The default behavior of this plugin is to create a schema for the table,
based on the current metric (for both fields and tags). However, writing
subsequent metrics with additional fields or tags will result in errors.

If you wish the plugin to sync the column-schema for every metric,
specify the `table_update_template` setting in your config file.

> [!NOTE] The following snippet contains a generic query that your
> database may (or may not) support. Consult your database's
> documentation for proper syntax and table / column options.

```toml
# Save metrics to an SQL Database
[[outputs.sql]]
  ## Table update template
  table_update_template = "ALTER TABLE {TABLE} ADD COLUMN {COLUMN}"
```

## Driver-specific information

### go-sql-driver/mysql

MySQL default quoting differs from standard ANSI/ISO SQL quoting. You must use
MySQL's ANSI\_QUOTES mode with this plugin. You can enable this mode by using
the setting `init_sql = "SET sql_mode='ANSI_QUOTES';"` or through a command-line
option when running MySQL. See MySQL's docs for [details on
ANSI\_QUOTES][mysql-quotes] and [how to set the SQL mode][mysql-mode].

You can use a DSN of the format "username:password@tcp(host:port)/dbname". See
the [driver docs][mysql-driver] for details.

[mysql-quotes]: https://dev.mysql.com/doc/refman/8.0/en/sql-mode.html#sqlmode_ansi_quotes

[mysql-mode]: https://dev.mysql.com/doc/refman/8.0/en/sql-mode.html#sql-mode-setting

[mysql-driver]: https://github.com/go-sql-driver/mysql

### jackc/pgx

You can use a DSN of the format
"postgres://username:password@host:port/dbname". See the [driver
docs](https://github.com/jackc/pgx) for more details.

### modernc.org/sqlite

It is not supported on windows/386, mips, and mips64 platforms.

The DSN is a filename or url with scheme "file:". See the [driver
docs](https://modernc.org/sqlite) for details.

### clickhouse

#### DSN

Note that even when the DSN is specified as `https://` the `secure=true`
parameter is still required.

The plugin now uses clickhouse-go v2. If you're still using a DSN compatible
with v1 it will try to convert the DSN to the new format but as both schemata
are not fully equivalent some parameters might not work anymore. Please check
for warnings in your log file and refer to the
[v2 DSN documentation][v2-dsn-docs] for available options.

[v2-dsn-docs]: https://github.com/ClickHouse/clickhouse-go/tree/v2.30.2?tab=readme-ov-file#dsn

#### Metric type to SQL type conversion

The following configuration makes the mapping compatible with Clickhouse:

```toml
  [outputs.sql.convert]
    conversion_style     = "literal"
    integer              = "Int64"
    text                 = "String"
    timestamp            = "DateTime"
    defaultvalue         = "String"
    unsigned             = "UInt64"
    bool                 = "UInt8"
    real                 = "Float64"
```

See [ClickHouse data
types](https://clickhouse.com/docs/en/sql-reference/data-types/) for more info.

### microsoft/go-mssqldb

Telegraf doesn't have unit tests for go-mssqldb so it should be treated as
experimental.

### snowflakedb/gosnowflake

Telegraf doesn't have unit tests for gosnowflake so it should be treated as
experimental.
