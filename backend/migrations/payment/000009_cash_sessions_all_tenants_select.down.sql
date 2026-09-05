SET LOCAL role = app_migrator;

DROP POLICY IF EXISTS cash_sessions_all_tenants_select ON cash_sessions;
