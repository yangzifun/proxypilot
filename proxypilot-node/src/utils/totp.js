const crypto = require('crypto');
const qrcode = require('qrcode');

class TOTP {
  // Generate random secret (from mg_worker.js)
  static generateSecret() {
    const chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
    let secret = '';
    for (let i = 0; i < 20; i++) {
      secret += chars.charAt(Math.floor(Math.random() * chars.length));
    }
    return secret;
  }

  // Generate OTP code (from mg_worker.js)
  static generateOTP(secret, window = 0) {
    const time = Math.floor(Date.now() / 30000) + window;
    return this.generateOTPWithTime(secret, time);
  }

  // Generate OTP with specific time (from mg_worker.js)
  static generateOTPWithTime(secret, time) {
    const key = this.base32ToBytes(secret);
    const timeBuffer = Buffer.alloc(8);
    timeBuffer.writeUInt32BE(Math.floor(time / 0x100000000), 0);
    timeBuffer.writeUInt32BE(time % 0x100000000, 4);

    const hmac = crypto.createHmac('sha1', Buffer.from(key));
    hmac.update(timeBuffer);
    const signature = hmac.digest();

    const offset = signature[signature.length - 1] & 0xf;

    const binary = ((signature[offset] & 0x7f) << 24) |
      ((signature[offset + 1] & 0xff) << 16) |
      ((signature[offset + 2] & 0xff) << 8) |
      (signature[offset + 3] & 0xff);

    const otp = binary % 1000000;
    return otp.toString().padStart(6, '0');
  }

  // Base32 to bytes conversion (from mg_worker.js)
  static base32ToBytes(base32) {
    const chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
    const bytes = [];
    let bits = 0;
    let buffer = 0;

    for (let i = 0; i < base32.length; i++) {
      const char = base32.charAt(i);
      const value = chars.indexOf(char);
      if (value === -1) continue;

      buffer = (buffer << 5) | value;
      bits += 5;

      while (bits >= 8) {
        bits -= 8;
        bytes.push((buffer >> bits) & 0xff);
        buffer &= (1 << bits) - 1;
      }
    }

    return bytes;
  }

  // Verify OTP code (from mg_worker.js)
  static async verify(secret, code, window = 1) {
    for (let i = -window; i <= window; i++) {
      const genCode = await this.generateOTP(secret, i);
      if (genCode === code) return true;
    }
    return false;
  }

  // Generate QR code URL (from mg_worker.js)
  static generateOTPAuthURL(username, secret, issuer = 'YZFN Admin') {
    const encodedIssuer = encodeURIComponent(issuer);
    const encodedUsername = encodeURIComponent(username);
    return `otpauth://totp/${encodedIssuer}:${encodedUsername}?secret=${secret}&issuer=${encodedIssuer}&algorithm=SHA1&digits=6&period=30`;
  }

  // Generate QR code image data URL
  static async generateQRCodeDataURL(username, secret, issuer = 'YZFN Admin') {
    const otpauthUrl = this.generateOTPAuthURL(username, secret, issuer);
    try {
      return await qrcode.toDataURL(otpauthUrl);
    } catch (error) {
      console.error('Failed to generate QR code:', error);
      return null;
    }
  }

  // Generate backup codes (from mg_worker.js)
  static generateBackupCodes(count = 10) {
    const codes = [];
    for (let i = 0; i < count; i++) {
      codes.push(
        Math.random().toString(36).substring(2, 10).toUpperCase()
      );
    }
    return codes;
  }

  // Validate backup code format
  static validateBackupCode(code) {
    if (!code || typeof code !== 'string') return false;
    return /^[A-Z0-9]{6,10}$/.test(code.trim().toUpperCase());
  }

  // Generate TOTP server time for debugging
  static getServerTime() {
    return Math.floor(Date.now() / 30000);
  }

  // Check if code is valid within tolerance window
  static async isValidCode(secret, code, tolerance = 1) {
    if (!secret || !code || code.length !== 6) {
      return false;
    }

    const normalizedCode = code.trim();
    if (!/^\d{6}$/.test(normalizedCode)) {
      return false;
    }

    return await this.verify(secret, normalizedCode, tolerance);
  }

  // Get time remaining for current code
  static getTimeRemaining() {
    const now = Date.now();
    const period = 30000; // 30 seconds
    const elapsed = now % period;
    return Math.floor((period - elapsed) / 1000);
  }
}

// Utility functions for MFA
async function setupMFA(username) {
  const secret = TOTP.generateSecret();
  const otpauthUrl = TOTP.generateOTPAuthURL(username, secret);
  const qrCodeUrl = await TOTP.generateQRCodeDataURL(username, secret);
  const backupCodes = TOTP.generateBackupCodes(10);

  return {
    secret,
    otpauthUrl,
    qrCodeUrl,
    backupCodes,
    instructions: {
      manual: `手动输入密钥: ${secret}`,
      qr: '使用身份验证器App扫描二维码',
      backup: '请妥善保管备份码'
    }
  };
}

async function verifyMFACode(secret, code) {
  return await TOTP.verify(secret, code);
}

async function verifyAndSetupMFA(username, code, secret) {
  const isValid = await verifyMFACode(secret, code);
  if (!isValid) {
    throw new Error('验证码无效');
  }

  const backupCodes = TOTP.generateBackupCodes(10);
  return {
    success: true,
    secret,
    backupCodes,
    message: 'MFA设置成功'
  };
}

module.exports = {
  TOTP,
  setupMFA,
  verifyMFACode,
  verifyAndSetupMFA
};