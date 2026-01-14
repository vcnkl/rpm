const repoRoot = process.env.REPO_ROOT || "";
const bundleRoot = process.env.BUNDLE_ROOT || "";
const tsVar = process.env.TS_VAR || "";

console.log(`REPO_ROOT=${repoRoot}`);
console.log(`BUNDLE_ROOT=${bundleRoot}`);
console.log(`TS_VAR=${tsVar}`);
