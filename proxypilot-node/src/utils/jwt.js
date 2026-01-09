const jwt = require('jsonwebtoken');
const crypto = require('crypto');

const DEFAULT_JWT_SECRET = process.env.JWT_SECRET || 'your-default-jwt-secret-change-this';
const JWT_EXPIRES_IN = process.env.JWT_EXPIRES_IN || 86400; // 24 hours

// Password hash function (from mg_worker.js)
async function hashPassword(password) {
  const msgBuffer = new TextEncoder().encode(password);
  const hashBuffer = await crypto.subtle.digest('SHA-256', msgBuffer);
  const hashArray = Array.from(new Uint8Array(hashBuffer));
  return hashArray.map(b => b.toString(16).padStart(2, '0')).join('');
}

// Sign JWT (adapted from mg_worker.js)
function signJwt(payload, secret = DEFAULT_JWT_SECRET) {
  return jwt.sign(payload, secret, {
    expiresIn: JWT_EXPIRES_IN,
    algorithm: 'HS256'
  });
}

// Verify JWT (adapted from mg_worker.js)
function verifyJwt(token, secret = DEFAULT_JWT_SECRET) {
  try {
    return jwt.verify(token, secret, { algorithms: ['HS256'] });
  } catch (error) {
    return false;
  }
}

// Generate token for admin user
function generateAdminToken(username, requiresMfa = false, mfaVerified = false) {
  const payload = {
    sub: username,
    exp: Math.floor(Date.now() / 1000) + JWT_EXPIRES_IN,
    requires_mfa: requiresMfa,
    mfa_verified: mfaVerified,
    role: 'admin'
  };
  
  return signJwt(payload);
}

// Generate token for API access
function generateApiToken(uuid, accessType = 'read') {
  const payload = {
    sub: uuid,
    exp: Math.floor(Date.now() / 1000) + JWT_EXPIRES_IN,
    access_type: accessType,
    role: 'api'
  };
  
  return signJwt(payload);
}

// Middleware to verify admin token
function verifyAdminToken(req, res, next) {
  const authHeader = req.headers.authorization;
  
  if (!authHeader || !authHeader.startsWith('Bearer ')) {
    return res.status(401).json({ 
      error: '未授权，请重新登录',
      success: false 
    });
  }
  
  const token = authHeader.split(' ')[1];
  const payload = verifyJwt(token);
  
  if (!payload || payload.role !== 'admin') {
    return res.status(401).json({ 
      error: 'Token 无效或已过期',
      success: false 
    });
  }
  
  // Check if MFA is required but not verified
  if (payload.requires_mfa && !payload.mfa_verified) {
    return res.status(403).json({ 
      error: '需要完成双重验证',
      success: false,
      requires_mfa: true
    });
  }
  
  req.user = payload;
  next();
}

// Middleware to verify API token
function verifyApiToken(req, res, next) {
  const authHeader = req.headers.authorization;
  const uuid = req.params.uuid || req.body.uuid;
  
  if (!authHeader || !authHeader.startsWith('Bearer ')) {
    return res.status(401).json({ 
      error: '需要API令牌',
      success: false 
    });
  }
  
  const token = authHeader.split(' ')[1];
  const payload = verifyJwt(token);
  
  if (!payload || payload.role !== 'api' || payload.sub !== uuid) {
    return res.status(401).json({ 
      error: 'API令牌无效',
      success: false 
    });
  }
  
  req.apiUser = payload;
  next();
}

// Generate refresh token
function generateRefreshToken(username) {
  const payload = {
    sub: username,
    type: 'refresh',
    exp: Math.floor(Date.now() / 1000) + (JWT_EXPIRES_IN * 7) // 7 days
  };
  
  return signJwt(payload);
}

// Verify refresh token
function verifyRefreshToken(token) {
  try {
    const payload = jwt.verify(token, DEFAULT_JWT_SECRET);
    return payload.type === 'refresh' ? payload : false;
  } catch (error) {
    return false;
  }
}

module.exports = {
  hashPassword,
  signJwt,
  verifyJwt,
  generateAdminToken,
  generateApiToken,
  generateRefreshToken,
  verifyRefreshToken,
  verifyAdminToken,
  verifyApiToken,
  DEFAULT_JWT_SECRET
};