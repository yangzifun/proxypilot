require('dotenv').config();
const express = require('express');
const cors = require('cors');
const helmet = require('helmet');
const morgan = require('morgan');
const path = require('path');
const cron = require('cron');

const { db, testConnection } = require('./database/connection');
const runMigrations = require('./database/migrate');
const seedDatabase = require('./database/seed');

// Import routes
const configRoutes = require('./routes/api/configs');
const subscriptionRoutes = require('./routes/api/subscription');
const adminRoutes = require('./routes/api/admin');
const templateRoutes = require('./routes/api/templates');
const ipManagementRoutes = require('./routes/api/ip-management');
const pagesRoutes = require('./routes/pages');

const app = express();
const PORT = process.env.PORT || 3000;

// Middleware
app.use(helmet({
  contentSecurityPolicy: false, // Disable CSP for development
}));
app.use(cors({
  origin: process.env.CORS_ORIGIN || '*',
  credentials: true
}));
app.use(express.json({ limit: '10mb' }));
app.use(express.urlencoded({ extended: true }));
app.use(morgan('dev'));

// Serve static files
app.use(express.static(path.join(__dirname, '../public')));
app.use('/assets', express.static(path.join(__dirname, '../public/assets')));

// Routes
app.use('/api/configs', configRoutes);
app.use('/api/subscription', subscriptionRoutes);
app.use('/api/admin', adminRoutes);
app.use('/api/templates', templateRoutes);
app.use('/api/ip-management', ipManagementRoutes);
app.use('/', pagesRoutes);

// Error handling middleware
app.use((err, req, res, next) => {
  console.error('Error:', err.stack);
  res.status(err.status || 500).json({
    error: err.message || 'Internal server error',
    success: false
  });
});

// 404 handler
app.use((req, res) => {
  res.status(404).json({
    error: 'Endpoint not found',
    success: false
  });
});

// Health check endpoint
app.get('/health', (req, res) => {
  res.json({
    status: 'ok',
    timestamp: new Date().toISOString(),
    uptime: process.uptime()
  });
});

// Initialize database and start server
async function initialize() {
  try {
    console.log('Initializing ProxyPilot Node.js application...');
    
    // Test database connection
    const connected = await testConnection();
    if (!connected) {
      console.warn('Database connection failed. Application will start without database support.');
    } else {
      // Run migrations
      await runMigrations();
      
      // Seed database with initial data
      await seedDatabase();
      
      console.log('Database initialization completed.');
    }

    // Start IP update cron job
    startIPUpdateCronJob();

    // Start server
    app.listen(PORT, () => {
      console.log(`✅ Server running on http://localhost:${PORT}`);
      console.log(`📊 Health check: http://localhost:${PORT}/health`);
      console.log(`🔐 Admin panel: http://localhost:${PORT}/admin`);
      console.log(`📱 User panel: http://localhost:${PORT}/`);
    });

  } catch (error) {
    console.error('Initialization failed:', error);
    process.exit(1);
  }
}

// IP update cron job
function startIPUpdateCronJob() {
  const schedule = process.env.IP_UPDATE_SCHEDULE || '0 */6 * * *';
  
  try {
    const job = new cron.CronJob(schedule, async () => {
      console.log('Running scheduled IP update...');
      try {
        const ipService = require('./services/ipUpdateService');
        const result = await ipService.runIpUpdateTask();
        console.log('Scheduled IP update completed:', result.message);
      } catch (error) {
        console.error('Scheduled IP update failed:', error.message);
      }
    });
    
    job.start();
    console.log(`IP update cron job scheduled: ${schedule}`);
  } catch (error) {
    console.error('Failed to start IP update cron job:', error.message);
  }
}

// Handle graceful shutdown
process.on('SIGTERM', gracefulShutdown);
process.on('SIGINT', gracefulShutdown);

function gracefulShutdown() {
  console.log('Shutting down gracefully...');
  
  // Close database connections
  if (db) {
    db.destroy().then(() => {
      console.log('Database connections closed.');
      process.exit(0);
    }).catch(err => {
      console.error('Error closing database connections:', err);
      process.exit(1);
    });
  } else {
    process.exit(0);
  }
}

// Start the application
initialize();