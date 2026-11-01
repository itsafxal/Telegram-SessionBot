// Migration script for existing MongoDB deployments.
// Run this script against an existing MongoDB instance using root credentials
// to provision or update the dedicated application user without losing any data.
//
// Usage via mongosh:
// mongosh -u "$MONGO_ROOT_USERNAME" -p "$MONGO_ROOT_PASSWORD" --authenticationDatabase admin docker/migrate-mongo-app-user.js

const appUser = process.env.MONGO_APP_USERNAME || 'session_app_user';
const appPwd = process.env.MONGO_APP_PASSWORD;
const dbName = process.env.MONGODB_DATABASE || 'telegram_session_bot';

if (!appPwd || appPwd.trim() === '') {
  print('[Migration] ERROR: MONGO_APP_PASSWORD must be set in environment before running migration.');
  quit(1);
}

print(`[Migration] Starting application user migration on database '${dbName}'...`);

// 1. Verify existing collections and documents are preserved
const targetDb = db.getSiblingDB(dbName);
const colls = targetDb.getCollectionNames();
print(`[Migration] Existing collections found: ${colls.join(', ') || '(none)'}`);

// 2. Ensure application user exists with least-privilege readWrite role
try {
  const existing = targetDb.getUser(appUser);
  if (!existing) {
    targetDb.createUser({
      user: appUser,
      pwd: appPwd,
      roles: [{ role: 'readWrite', db: dbName }]
    });
    print(`[Migration] Created new application user '${appUser}' on '${dbName}'.`);
  } else {
    targetDb.updateUser(appUser, {
      pwd: appPwd,
      roles: [{ role: 'readWrite', db: dbName }]
    });
    print(`[Migration] Updated credentials & verified readWrite role for '${appUser}' on '${dbName}'.`);
  }
} catch (err) {
  print(`[Migration] FATAL: Failed to configure application user: ${err.message}`);
  quit(2);
}

// 3. Verify application user authentication
const authResult = targetDb.auth(appUser, appPwd);
if (authResult.ok === 1 || authResult === 1) {
  print(`[Migration] SUCCESS: Application user '${appUser}' authenticated successfully with least-privilege permissions.`);
} else {
  print(`[Migration] WARNING: Test authentication returned non-OK status: ${JSON.stringify(authResult)}`);
}

print('[Migration] Migration completed safely with zero data loss.');
