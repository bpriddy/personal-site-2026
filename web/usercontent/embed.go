// Package usercontent embeds the files the user-content service serves itself
// (not part of any front end): the in-iframe host API script.
package usercontent

import _ "embed"

// SiteHostJS is served at /site-host.js on the user-content domain. The literal
// placeholder __MAIN_ORIGIN__ is replaced with the main site's origin at startup.
//
//go:embed site-host.js
var SiteHostJS string
