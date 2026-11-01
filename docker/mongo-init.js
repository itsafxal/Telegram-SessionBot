// Mongo initialization script for dedicated application user creation.
// Executed only upon the initialization of a fresh database volume.
const appUser = process.env.MONGO_APP_USERNAME || 'session_app_user';
const appPwd = process.env.MONGO_APP_PASSWORD;
const dbName = process.env.MONGO_INITDB_DATABASE || 'telegram_session_bot';

if (appPwd) {
  const targetDb = db.getSiblingDB(dbName);
  try {
    targetDb.createUser({
      user: appUser,
      pwd: appPwd,
      roles: [{ role: 'readWrite', db: dbName }]
    });
    print(`[MongoDB Init] Successfully created dedicated least-privilege application user: ${appUser} on database: ${dbName}`);
  } catch (e) {
    print(`[MongoDB Init] User may already exist: ${e}`);
  }
} else {
  print('[MongoDB Init] MONGO_APP_PASSWORD not set; skipping dedicated app user creation');
}

