const { verifyAdminToken, verifyApiToken } = require('../utils/jwt');
const { db } = require('../database/connection');

// Admin authentication middleware
const requireAdminAuth = verifyAdminToken;

// API authentication middleware
const requireApiAuth = verifyApiToken;

// Check if user is authenticated (for pages)
async function checkAuth(req, res, next) {
  const token = req.cookies?.token || req.headers.authorization?.replace('Bearer ', '');
  
  if (!token) {
    req.isAuthenticated = false;
    return next();
  }
  
  try {
    const { verifyJwt } = require('../utils/jwt');
    const payload = verifyJwt(token);
    
    if (payload && payload.role === 'admin') {
      // For admin users, check if they exist in database
      const user = await db('admin_users')
        .where({ username: payload.sub })
        .first();
      
      if (user) {
        req.user = {
          username: user.username,
          mfa_enabled: user.mfa_enabled === 1,
          requires_mfa: payload.requires_mfa,
          mfa_verified: payload.mfa_verified
        };
        req.isAuthenticated = true;
      } else {
        req.isAuthenticated = false;
      }
    } else if (payload && payload.role === 'api') {
      // For API users
      req.apiUser = payload;
      req.isAuthenticated = true;
    } else {
      req.isAuthenticated = false;
    }
  } catch (error) {
    req.isAuthenticated = false;
  }
  
  next();
}

// Require authenticated user (redirect to login if not authenticated)
function requireAuth(req, res, next) {
  if (!req.isAuthenticated) {
    if (req.path.startsWith('/api')) {
      return res.status(401).json({ 
        error: '需要登录',
        success: false 
      });
    } else {
      return res.redirect('/login');
    }
  }
  next();
}

// Require admin role
async function requireAdminRole(req, res, next) {
  if (!req.isAuthenticated || !req.user) {
    if (req.path.startsWith('/api')) {
      return res.status(401).json({ 
        error: '需要管理员权限',
        success: false 
      });
    } else {
      return res.redirect('/login');
    }
  }
  
  // Check if user is admin (additional verification)
  const user = await db('admin_users')
    .where({ username: req.user.username })
    .first();
  
  if (!user) {
    if (req.path.startsWith('/api')) {
      return res.status(403).json({ 
        error: '权限不足',
        success: false 
      });
    } else {
      return res.redirect('/login');
    }
  }
  
  next();
}

// Require MFA verification
function requireMFA(req, res, next) {
  if (!req.user) {
    return res.status(401).json({ 
      error: '需要登录',
      success: false 
    });
  }
  
  if (req.user.mfa_enabled && !req.user.mfa_verified) {
    return res.status(403).json({ 
      error: '需要完成双重验证',
      success: false,
      requires_mfa: true
    });
  }
  
  next();
}

// Rate limiting middleware
const rateLimit = require('express-rate-limit');

const apiLimiter = rateLimit({
  windowMs: 15 * 60 * 1000, // 15 minutes
  max: 100, // Limit each IP to 100 requests per windowMs
  message: { error: '请求过于频繁，请稍后再试', success: false },
  skip: (req) => {
    // Skip rate limiting for admin routes
    return req.path.startsWith('/api/admin') || req.path.startsWith('/admin');
  }
});

const authLimiter = rateLimit({
  windowMs: 15 * 60 * 1000, // 15 minutes
  max: 10, // Limit each IP to 10 login attempts per windowMs
  message: { error: '登录尝试过多，请稍后再试', success: false }
});

// Validate request parameters
function validateRequest(schema) {
  return (req, res, next) => {
    const validationResult = schema.validate(req.body);
    
    if (validationResult.error) {
      return res.status(400).json({
        error: validationResult.error.details[0].message,
        success: false
      });
    }
    
    req.validatedData = validationResult.value;
    next();
  };
}

// Logging middleware
function requestLogger(req, res, next) {
  const startTime = Date.now();
  
  res.on('finish', () => {
    const duration = Date.now() - startTime;
    const logMessage = `${req.method} ${req.originalUrl} ${res.statusCode} ${duration}ms`;
    
    if (res.statusCode >= 400) {
      console.warn(logMessage);
    } else {
      console.log(logMessage);
    }
  });
  
  next();
}

// CORS middleware for API
function apiCors(req, res, next) {
  res.header('Access-Control-Allow-Origin', process.env.CORS_ORIGIN || '*');
  res.header('Access-Control-Allow-Headers', 'Origin, X-Requested-With, Content-Type, Accept, Authorization');
  res.header('Access-Control-Allow-Methods', 'GET, POST, PUT, DELETE, OPTIONS');
  res.header('Access-Control-Allow-Credentials', 'true');
  
  if (req.method === 'OPTIONS') {
    return res.status(200).end();
  }
  
  next();
}

module.exports = {
  requireAdminAuth,
  requireApiAuth,
  checkAuth,
  requireAuth,
  requireAdminRole,
  requireMFA,
  apiLimiter,
  authLimiter,
  validateRequest,
  requestLogger,
  apiCors
};