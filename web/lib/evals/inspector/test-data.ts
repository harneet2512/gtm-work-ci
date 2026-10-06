// Test data is never shown as real (HAR-149): when the web app runs against a fixture core (the e2e stack), every inspector page says
// TEST DATA and episodes read "Sample episode (test data)", so no screenshot of fixture content can pass for a recorded run.
export const isTestData = (env: Readonly<Record<string, string | undefined>> = process.env): boolean => env.GTM_TEST_DATA === "1";

export const TEST_DATA_TITLE = "Sample episode (test data)";
