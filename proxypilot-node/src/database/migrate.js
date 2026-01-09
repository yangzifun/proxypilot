const { db } = require('./connection');

async function runMigrations() {
  console.log('Running database migrations...');
  
  try {
    // 1. Create configs table
    await db.schema.createTableIfNotExists('configs', (table) => {
      table.increments('id').primary();
      table.string('uuid').notNullable();
      table.string('ip');
      table.string('domain');
      table.integer('port');
      table.text('meta'); // JSON string of protocol details
      table.bigInteger('created_at').defaultTo(db.raw('EXTRACT(EPOCH FROM NOW()) * 1000'));
      table.bigInteger('updated_at').defaultTo(db.raw('EXTRACT(EPOCH FROM NOW()) * 1000'));
      table.index('uuid', 'idx_configs_uuid');
    });

    // 2. Create config_access_logs table
    await db.schema.createTableIfNotExists('config_access_logs', (table) => {
      table.increments('id').primary();
      table.string('uuid').notNullable();
      table.string('query_type').notNullable(); // 'subscription' or 'api-generation'
      table.string('client_ip');
      table.text('user_agent');
      table.timestamp('created_at').defaultTo(db.fn.now());
      table.index('uuid', 'idx_access_logs_uuid');
      table.index('created_at', 'idx_access_logs_date');
    });

    // 3. Create cfips table
    await db.schema.createTableIfNotExists('cfips', (table) => {
      table.increments('id').primary();
      table.string('ip').unique().notNullable();
      table.string('ip_type'); // 'v4' or 'v6'
      table.string('carrier');
      table.string('source');
      table.bigInteger('created_at').defaultTo(db.raw('EXTRACT(EPOCH FROM NOW()) * 1000'));
      table.bigInteger('updated_at').defaultTo(db.raw('EXTRACT(EPOCH FROM NOW()) * 1000'));
    });

    // 4. Create cf_domains table
    await db.schema.createTableIfNotExists('cf_domains', (table) => {
      table.increments('id').primary();
      table.string('domain').unique().notNullable();
      table.text('remark');
      table.bigInteger('created_at').defaultTo(db.raw('EXTRACT(EPOCH FROM NOW()) * 1000'));
      table.bigInteger('updated_at').defaultTo(db.raw('EXTRACT(EPOCH FROM NOW()) * 1000'));
    });

    // 5. Create edgeone_domains table
    await db.schema.createTableIfNotExists('edgeone_domains', (table) => {
      table.increments('id').primary();
      table.string('domain').unique().notNullable();
      table.text('remark');
      table.bigInteger('created_at').defaultTo(db.raw('EXTRACT(EPOCH FROM NOW()) * 1000'));
      table.bigInteger('updated_at').defaultTo(db.raw('EXTRACT(EPOCH FROM NOW()) * 1000'));
    });

    // 6. Create admin_users table
    await db.schema.createTableIfNotExists('admin_users', (table) => {
      table.increments('id').primary();
      table.string('username').unique().notNullable();
      table.string('password_hash').notNullable();
      table.integer('mfa_enabled').defaultTo(0);
      table.string('mfa_secret');
      table.bigInteger('last_mfa_login').defaultTo(0);
      table.bigInteger('last_backup_login').defaultTo(0);
      table.bigInteger('created_at').defaultTo(db.raw('EXTRACT(EPOCH FROM NOW()) * 1000'));
    });

    // 7. Create mfa_backup_codes table
    await db.schema.createTableIfNotExists('mfa_backup_codes', (table) => {
      table.increments('id').primary();
      table.string('username').notNullable();
      table.string('code').unique().notNullable();
      table.bigInteger('created_at').defaultTo(db.raw('EXTRACT(EPOCH FROM NOW()) * 1000'));
      table.integer('used').defaultTo(0);
      table.bigInteger('used_at').defaultTo(0);
    });

    // 8. Create config_templates table
    await db.schema.createTableIfNotExists('config_templates', (table) => {
      table.increments('id').primary();
      table.string('name').unique().notNullable();
      table.text('url').notNullable();
      table.text('description');
      table.integer('is_default').defaultTo(0);
      table.bigInteger('created_at').defaultTo(db.raw('EXTRACT(EPOCH FROM NOW()) * 1000'));
      table.bigInteger('updated_at').defaultTo(db.raw('EXTRACT(EPOCH FROM NOW()) * 1000'));
    });

    // 9. Create auto_update_settings table
    await db.schema.createTableIfNotExists('auto_update_settings', (table) => {
      table.string('source').primary();
      table.integer('enabled').notNullable();
      table.bigInteger('updated_at').defaultTo(db.raw('EXTRACT(EPOCH FROM NOW()) * 1000'));
    });

    console.log('All migrations completed successfully.');
  } catch (error) {
    console.error('Migration failed:', error.message);
    throw error;
  }
}

// Run migrations if called directly
if (require.main === module) {
  runMigrations()
    .then(() => process.exit(0))
    .catch((error) => {
      console.error(error);
      process.exit(1);
    });
}

module.exports = runMigrations;