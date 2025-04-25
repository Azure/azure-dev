process.env.AZD_SERVER = process.env.AZD_SERVER || '127.0.0.1:46464';
process.env.AZD_ACCESS_TOKEN = process.env.AZD_ACCESS_TOKEN || 'dev-token';

require('./index');
