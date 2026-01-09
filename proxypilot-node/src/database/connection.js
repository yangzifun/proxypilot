const knex = require('knex');
const config = require('./knexfile');

const environment = process.env.NODE_ENV || 'development';
const db = knex(config[environment]);

// Test connection
async function testConnection() {
  try {
    await db.raw('SELECT 1');
    console.log('Database connection established successfully.');
    return true;
  } catch (error) {
    console.error('Failed to connect to database:', error.message);
    return false;
  }
}

module.exports = {
  db,
  testConnection
};