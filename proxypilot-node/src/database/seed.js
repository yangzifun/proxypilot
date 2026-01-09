const { db } = require('./connection');
const bcrypt = require('bcrypt');

async function seedDatabase() {
  console.log('Seeding database...');
  
  try {
    // 1. Create default admin user
    const defaultUsername = process.env.DEFAULT_ADMIN_USERNAME || 'admin';
    const defaultPassword = process.env.DEFAULT_ADMIN_PASSWORD || 'admin123';
    const passwordHash = await bcrypt.hash(defaultPassword, 10);
    
    await db('admin_users').insert({
      username: defaultUsername,
      password_hash: passwordHash,
      mfa_enabled: 0,
      created_at: Date.now()
    }).onConflict('username').ignore();
    
    console.log(`Default admin user created: ${defaultUsername}`);

    // 2. Insert default auto update settings
    const defaultSettings = [
      { source: 'global_enabled', enabled: 1 },
      { source: 'hostmonit_v4', enabled: 1 },
      { source: 'hostmonit_v6', enabled: 0 },
      { source: 'vps789', enabled: 1 },
      { source: 'last_executed', enabled: 0 }
    ];

    for (const setting of defaultSettings) {
      await db('auto_update_settings').insert({
        ...setting,
        updated_at: Date.now()
      }).onConflict('source').merge();
    }

    console.log('Default auto update settings inserted');

    // 3. Insert example config template
    await db('config_templates').insert({
      name: '默认模板',
      url: 'https://s3.yangzifun.org/links/yaml/clash-all.yaml',
      description: '默认配置模板',
      is_default: 1,
      created_at: Date.now(),
      updated_at: Date.now()
    }).onConflict('name').ignore();

    console.log('Default config template inserted');

    // 4. Insert example domains
    const exampleDomains = [
      { domain: 'cf.example.com', remark: 'Cloudflare示例域名', source: 'Cloudflare' },
      { domain: 'edgeone.example.com', remark: 'EdgeOne示例域名', source: '腾讯云EdgeOne' }
    ];

    for (const domain of exampleDomains) {
      if (domain.source === 'Cloudflare') {
        await db('cf_domains').insert({
          domain: domain.domain,
          remark: domain.remark,
          created_at: Date.now(),
          updated_at: Date.now()
        }).onConflict('domain').ignore();
      } else {
        await db('edgeone_domains').insert({
          domain: domain.domain,
          remark: domain.remark,
          created_at: Date.now(),
          updated_at: Date.now()
        }).onConflict('domain').ignore();
      }
    }

    console.log('Example domains inserted');

    // 5. Insert example IPs
    const exampleIPs = [
      { ip: '104.16.132.229', ip_type: 'v4', carrier: 'CM', source: 'hostmonit_v4' },
      { ip: '2606:4700:90::a29f:9ce5', ip_type: 'v6', carrier: 'CT', source: 'hostmonit_v6' },
      { ip: '1.0.0.1', ip_type: 'v4', carrier: 'ALL', source: 'vps789' }
    ];

    for (const ip of exampleIPs) {
      await db('cfips').insert({
        ip: ip.ip,
        ip_type: ip.ip_type,
        carrier: ip.carrier,
        source: ip.source,
        created_at: Date.now(),
        updated_at: Date.now()
      }).onConflict('ip').ignore();
    }

    console.log('Example IPs inserted');

    console.log('Database seeding completed successfully.');
  } catch (error) {
    console.error('Seeding failed:', error.message);
    throw error;
  }
}

// Run seeding if called directly
if (require.main === module) {
  seedDatabase()
    .then(() => process.exit(0))
    .catch((error) => {
      console.error(error);
      process.exit(1);
    });
}

module.exports = seedDatabase;