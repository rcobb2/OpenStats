// Enhanced Software Normalization and Deduplication Script for n8n
// This script processes inventory from multiple MDM sources and intelligently deduplicates

const items = $input.all();
const softwareMap = new Map(); // Key: normalized_name|platform|version, Value: aggregated data

// Helper function to normalize software names
function normalizeName(name) {
  if (!name) return '';
  
  let normalized = name;
  
  // Remove .app extension for macOS apps
  normalized = normalized.replace(/\.app$/i, '');
  
  // Remove common architecture suffixes
  normalized = normalized.replace(/\s+(x64|x86|32-bit|64-bit|32bit|64bit|ARM64)/gi, '');
  
  // Remove platform indicators
  normalized = normalized.replace(/\s+(for|on)\s+(Windows|macOS|Mac|Linux)/gi, '');
  
  // Remove year suffixes (e.g., "2024", "2025")
  normalized = normalized.replace(/\s+\d{4}$/g, '');
  
  // Remove version numbers from the name itself
  normalized = normalized.replace(/\s+v?\d+(\.\d+)*/gi, '');
  
  // Remove "Microsoft" prefix duplication (e.g., "Microsoft Microsoft Word")
  normalized = normalized.replace(/^(Microsoft|Adobe|Google)\s+\1\s+/i, '$1 ');
  
  // Normalize common name variations
  const nameMap = {
    'Google Chrome': 'Google Chrome',
    'Chrome': 'Google Chrome',
    'Mozilla Firefox': 'Firefox',
    'Microsoft Edge': 'Edge',
    'Visual Studio Code': 'VS Code',
    'VSCode': 'VS Code',
    'Adobe Acrobat DC': 'Adobe Acrobat',
    'Adobe Acrobat Pro': 'Adobe Acrobat',
  };
  
  // Apply name normalization
  for (const [pattern, canonical] of Object.entries(nameMap)) {
    if (normalized.match(new RegExp(`^${pattern}`, 'i'))) {
      normalized = canonical;
      break;
    }
  }
  
  // Clean up extra whitespace
  normalized = normalized.replace(/\s+/g, ' ').trim();
  
  return normalized;
}

// Helper function to extract version
function extractVersion(versionString) {
  if (!versionString) return 'unknown';
  
  // Remove common prefixes
  let version = versionString.toString().trim();
  version = version.replace(/^v/i, '');
  
  // Extract semantic version if present
  const semverMatch = version.match(/\d+(\.\d+){0,3}/);
  if (semverMatch) {
    return semverMatch[0];
  }
  
  return version;
}

// Helper function to determine platform
function determinePlatform(data) {
  if (data.platform) return data.platform;
  if (data.osPlatform) return data.osPlatform;
  
  // Try to infer from other fields
  if (data.name && data.name.endsWith('.app')) return 'macOS';
  if (data.bundleId) return 'macOS';
  
  return 'Windows'; // Default
}

// Helper function to extract vendor
function extractVendor(data) {
  let vendor = data.vendor || data.publisher || '';
  
  // Infer vendor from bundleId for macOS apps
  if (!vendor && data.bundleId) {
    const bundleParts = data.bundleId.split('.');
    if (bundleParts.length >= 2) {
      // e.g., "com.microsoft.Word" -> "Microsoft"
      vendor = bundleParts[1].charAt(0).toUpperCase() + bundleParts[1].slice(1);
    }
  }
  
  // Infer vendor from software name
  if (!vendor) {
    const nameVendors = ['Microsoft', 'Google', 'Adobe', 'Apple', 'Mozilla'];
    for (const v of nameVendors) {
      if (data.software_name && data.software_name.includes(v)) {
        vendor = v;
        break;
      }
    }
  }
  
  return vendor;
}

// Process each item from all MDM sources
for (const item of items) {
  const data = item.json;
  
  // Determine the source
  let mdmSource = data.source || data.mdm_source || 'unknown';
  
  // Handle JAMF format (nested applications array)
  let apps = [];
  if (data.applications && Array.isArray(data.applications)) {
    // JAMF format
    mdmSource = 'jamf';
    apps = data.applications.map(app => ({
      software_name: app.name,
      version: app.version,
      bundleId: app.bundleId,
      platform: 'macOS',
      sizeMegabytes: app.sizeMegabytes,
      deviceId: data.id || data.udid
    }));
  } else {
    // Direct format from Intune/Defender
    apps = [{
      software_name: data.software_name || data.displayName || data.name,
      version: data.version || data.displayVersion,
      platform: determinePlatform(data),
      vendor: data.vendor || data.publisher,
      device_count: data.device_count || 1,
      deviceId: data.device_id || data.id
    }];
  }
  
  // Process each application
  for (const app of apps) {
    const rawName = app.software_name || '';
    
    // Skip if no name
    if (!rawName) continue;
    
    // Skip system components, updates, and other noise
    if (rawName.match(/update|hotfix|security|kb\d+|driver|component/i)) {
      continue;
    }
    
    // Skip Apple system apps that are on every Mac
    if (mdmSource === 'jamf' && rawName.match(/^(System|VoiceOver|Migration|Boot Camp|AirPort|Bluetooth|ColorSync|Digital Color|Audio MIDI|Grapher|Script Editor)/i)) {
      continue;
    }
    
    const normalizedName = normalizeName(rawName);
    const version = extractVersion(app.version);
    const platform = app.platform || 'Windows';
    const vendor = extractVendor({ ...app, software_name: normalizedName });
    
    // Create unique key: normalized_name|platform|version
    const key = `${normalizedName}|${platform}|${version}`;
    
    if (softwareMap.has(key)) {
      // Aggregate device count and sources
      const existing = softwareMap.get(key);
      existing.device_count += (app.device_count || 1);
      
      // Add source if not already present
      if (!existing.sources.includes(mdmSource)) {
        existing.sources.push(mdmSource);
      }
      
      // Add source_id if present
      if (app.bundleId && !existing.source_ids.includes(app.bundleId)) {
        existing.source_ids.push(app.bundleId);
      }
    } else {
      // New entry
      softwareMap.set(key, {
        software_name: normalizedName,
        current_version: version,
        platform: platform,
        vendor: vendor,
        device_count: app.device_count || 1,
        sources: [mdmSource],
        source_ids: app.bundleId ? [app.bundleId] : [],
        raw_name: rawName, // Keep original for reference
        size_mb: app.sizeMegabytes || null
      });
    }
  }
}

// Convert map to array of items for n8n
const normalized = Array.from(softwareMap.values()).map(data => ({
  json: data
}));

// Log statistics
console.log(`Processed ${items.length} input items`);
console.log(`Deduplicated to ${normalized.length} unique software entries`);
console.log(`Deduplication ratio: ${((1 - normalized.length / items.length) * 100).toFixed(1)}%`);

return normalized;
