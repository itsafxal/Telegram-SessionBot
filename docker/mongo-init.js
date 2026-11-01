// Mongo initialization script for dedicated application user creation.
// Executed upon the initialization of a fresh database volume or during user migration.
const appUser = process.env.MONGO_APP_USERNAME;
const appPwd = process.env.MONGO_APP_PASSWORD;
const dbName = process.env.MONGO_INITDB_DATABASE || 'telegram_session_bot';

if (!appUser || appUser.trim() === '') {
  throw new Error('[MongoDB Init] FATAL: MONGO_APP_USERNAME is required and cannot be empty');
}

if (!appPwd || appPwd.trim() === '') {
  throw new Error('[MongoDB Init] FATAL: MONGO_APP_PASSWORD is required and cannot be empty');
}

const targetDb = db.getSiblingDB(dbName);
try {
  const existingUser = targetDb.getUser(appUser);
  if (!existingUser) {
    targetDb.createUser({
      user: appUser,
      pwd: appPwd,
      roles: [{ role: 'readWrite', db: dbName }]
    });
    print(`[MongoDB Init] Successfully created dedicated least-privilege application user: ${appUser} on database: ${dbName}`);
  } else {
    targetDb.updateUser(appUser, {
      pwd: appPwd,
      roles: [{ role: 'readWrite', db: dbName }]
    });
    print(`[MongoDB Init] Verified/updated existing application user: ${appUser} with readWrite permissions on database: ${dbName}`);
  }
} catch (e) {
  print(`[MongoDB Init] User setup error: ${e.message}`);
  throw e;
}
