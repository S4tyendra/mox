// Javascript is generated from typescript, do not modify generated javascript because changes will be overwritten.

// We build CSS rules in JS. For several reasons:
// - To keep the style definitions closer to their use.
// - To make it easier to provide both light/regular and dark mode colors.
// - To use class names for styling, instead of the the many inline styles.
//   Makes it easier to look through a DOM, and easier to change the style of all
//   instances of a class.

import {dom, style, attr} from '../lib'
import * as api from './api'

const cssStyleDark = dom.style(attr.type('text/css'))
document.head.prepend(cssStyleDark)
const styleSheetDark = cssStyleDark.sheet!
styleSheetDark.insertRule('@media (prefers-color-scheme: dark) {}')
const darkModeRule = styleSheetDark.cssRules[0] as CSSMediaRule

// We keep the default/regular styles and dark-mode styles in separate stylesheets.
const cssStyle = dom.style(attr.type('text/css'))
document.head.prepend(cssStyle)
const styleSheet = cssStyle.sheet!

let cssRules: { [selector: string]: string} = {} // For ensuring a selector has a single definition.
// Ensure a selector has the given style properties. If a style value is an array,
// it must have 2 elements. The first is the default value, the second used for a
// rule for dark mode.
export const ensureCSS = (selector: string, styles: { [prop: string]: string | number | string[] }, important?: boolean) => {
	// Check that a selector isn't added again with different styling. Only during development.
	const checkConsistency = location.hostname === 'localhost'
	if (cssRules[selector]) {
		if (checkConsistency) {
			const exp = JSON.stringify(styles)
			if (cssRules[selector] !== exp) {
				throw new Error('duplicate css rule for selector '+selector+', had '+cssRules[selector] + ', next '+exp)
			}
		}
		return
	}
	cssRules[selector] = checkConsistency ? JSON.stringify(styles) : 'x'

	const index = styleSheet.cssRules.length
	styleSheet.insertRule(selector + ' {}', index)
	const st = (styleSheet.cssRules[index] as CSSStyleRule).style
	let darkst: CSSStyleDeclaration | undefined
	for (let [k, v] of Object.entries(styles)) {
		// We've kept the camel-case in our code which we had from when we did "st[prop] =
		// value". It is more convenient as object keys. So convert to kebab-case, but only
		// if this is not a css property.
		if (!k.startsWith('--')) {
			k = k.replace(/[A-Z]/g, s => '-'+s.toLowerCase())
		}
		if (Array.isArray(v)) {
			if (v.length !== 2) {
				throw new Error('2 elements required for light/dark mode style, got '+v.length)
			}
			if (!darkst) {
				const darkIndex = darkModeRule.cssRules.length
				darkModeRule.insertRule(selector + ' {}', darkIndex)
				darkst = (darkModeRule.cssRules[darkIndex] as CSSStyleRule).style
			}
			st.setProperty(k, ''+v[0], important ? 'important' : '')
			darkst.setProperty(k, ''+v[1], important ? 'important' : '')
		} else {
			st.setProperty(k, ''+v, important ? 'important' : '')
		}
	}
}

// Ensure CSS styling exists for a class, returning the same kind of object
// returned by dom._class, for use with dom.*-building functions.
export const css = (className: string, styles: { [prop: string]: string | number | string[] }, important?: boolean): { _class: string[] } => {
	ensureCSS('.'+className, styles, important)
	return dom._class(className)
}

// todo: reduce number of colors. hopefully we can derive some colors from a few base colors (making them brighter/darker, or shifting hue, etc). then make them configurable through settings.
// todo: add the standard padding and border-radius, perhaps more.
// We define css variables, making them easy to override.

// Base colour tokens. Each value is [light, dark]. These drive the automatic
// light/dark behaviour (:root + prefers-color-scheme) AND the forced-scheme
// rules (html.scheme-light / html.scheme-dark) added below.
const baseTokens: { [v: string]: string[] } = {
	'--color': ['#111827', '#f9fafb'],
	'--colorMild': ['#6b7280', '#9ca3af'],
	'--colorMilder': ['#9ca3af', '#6b7280'],
	'--backgroundColor': ['#ffffff', '#09090b'],
	'--backgroundColorMild': ['#f9fafb', '#121215'],
	'--backgroundColorMilder': ['#f3f4f6', '#1c1c21'],
	'--borderColor': ['rgba(0, 0, 0, 0.08)', 'rgba(255, 255, 255, 0.1)'],
	'--mailboxesTopBackgroundColor': ['rgba(255, 255, 255, 0.82)', 'rgba(9, 9, 11, 0.82)'],
	'--msglistBackgroundColor': ['#ffffff', '#09090b'],
	'--boxShadow': ['0 1px 3px 0 rgba(0, 0, 0, 0.05), 0 1px 2px -1px rgba(0, 0, 0, 0.05)', '0 1px 3px 0 rgba(0, 0, 0, 0.4)'],
	'--shadowSm': ['0 1px 2px 0 rgba(0, 0, 0, 0.05)', '0 1px 2px 0 rgba(0, 0, 0, 0.3)'],
	'--shadowMd': ['0 4px 6px -1px rgba(0, 0, 0, 0.07), 0 2px 4px -2px rgba(0, 0, 0, 0.05)', '0 4px 6px -1px rgba(0, 0, 0, 0.4), 0 2px 4px -2px rgba(0, 0, 0, 0.3)'],
	'--shadowLg': ['0 10px 15px -3px rgba(0, 0, 0, 0.08), 0 4px 6px -4px rgba(0, 0, 0, 0.04)', '0 10px 15px -3px rgba(0, 0, 0, 0.5), 0 4px 6px -4px rgba(0, 0, 0, 0.4)'],
	'--shadowXl': ['0 20px 25px -5px rgba(0, 0, 0, 0.1), 0 8px 10px -6px rgba(0, 0, 0, 0.04)', '0 20px 25px -5px rgba(0, 0, 0, 0.6), 0 8px 10px -6px rgba(0, 0, 0, 0.5)'],
	'--buttonBackground': ['#f3f4f6', '#1c1c21'],
	'--buttonBorderColor': ['rgba(0, 0, 0, 0.08)', 'rgba(255, 255, 255, 0.1)'],
	'--buttonHoverBackground': ['#e5e7eb', '#27272f'],
	'--overlayOpaqueBackgroundColor': ['#ffffff', '#09090b'],
	'--overlayBackgroundColor': ['rgba(0, 0, 0, 0.35)', 'rgba(0, 0, 0, 0.65)'],
	'--popupColor': ['#111827', '#f9fafb'],
	'--popupBackgroundColor': ['rgba(255, 255, 255, 0.95)', 'rgba(18, 18, 22, 0.95)'],
	'--popupBorderColor': ['rgba(0, 0, 0, 0.1)', 'rgba(255, 255, 255, 0.12)'],
	'--highlightBackground': ['#2563eb', '#3b82f6'],
	'--highlightBorderColor': ['#1d4ed8', '#60a5fa'],
	'--highlightBackgroundHover': ['#1d4ed8', '#2563eb'],
	'--mailboxActiveBackground': ['rgba(37, 99, 235, 0.08)', 'rgba(59, 130, 246, 0.14)'],
	'--mailboxHoverBackgroundColor': ['rgba(0, 0, 0, 0.04)', 'rgba(255, 255, 255, 0.05)'],
	'--msgItemActiveBackground': ['rgba(37, 99, 235, 0.09)', 'rgba(59, 130, 246, 0.15)'],
	'--msgItemHoverBackgroundColor': ['rgba(0, 0, 0, 0.03)', 'rgba(255, 255, 255, 0.04)'],
	'--msgItemFocusBorderColor': ['#2563eb', '#3b82f6'],
	'--buttonTristateOnBackground': ['#16a34a', '#22c55e'],
	'--buttonTristateOffBackground': ['#dc2626', '#ef4444'],
	'--warningBackgroundColor': ['#fef3c7', '#78350f'],
	'--successBackground': ['#dcfce7', '#14532d'],
	'--emphasisBackground': ['#374151', '#9ca3af'],
	'--underlineGreen': ['#16a34a', '#22c55e'],
	'--underlineRed': ['#dc2626', '#ef4444'],
	'--underlineBlue': ['#2563eb', '#3b82f6'],
	'--underlineGrey': ['#6b7280', '#9ca3af'],
	'--quoted1Color': ['#0284c7', '#38bdf8'],
	'--quoted2Color': ['#7c3aed', '#a78bfa'],
	'--quoted3Color': ['#059669', '#34d399'],
	'--scriptSwitchUnderlineColor': ['#d97706', '#f59e0b'],
	'--linkColor': ['#2563eb', '#60a5fa'],
	'--linkVisitedColor': ['#7c3aed', '#c084fc'],

	'--accent': ['#111827', '#f9fafb'],
	'--accentText': ['#ffffff', '#111827'],
	'--radius': ['8px', '8px'],
	'--radiusSm': ['6px', '6px'],
	'--radiusMd': ['10px', '10px'],
	'--radiusLg': ['12px', '12px'],
	'--radiusFull': ['9999px', '9999px'],
	'--easeOut': ['cubic-bezier(0.32, 0.72, 0, 1)', 'cubic-bezier(0.32, 0.72, 0, 1)'],
}

ensureCSS(':root', baseTokens)

// pickScheme returns a single-valued token map (light=index 0, dark=index 1)
// for use in a forced-scheme rule.
const pickScheme = (i: number): { [v: string]: string } => {
	const m: { [v: string]: string } = {}
	for (const [k, v] of Object.entries(baseTokens)) {
		m[k] = v[i]
	}
	return m
}

// High-contrast token maps: maximal contrast, solid borders, no faint tints.
const hcLightTokens: { [v: string]: string } = {
	'--color': '#000000', '--colorMild': '#000000', '--colorMilder': '#1a1a1a',
	'--backgroundColor': '#ffffff', '--backgroundColorMild': '#ffffff', '--backgroundColorMilder': '#000000',
	'--borderColor': '#000000', '--mailboxesTopBackgroundColor': '#ffffff', '--msglistBackgroundColor': '#ffffff',
	'--boxShadow': '0 0 0 1px #000', '--shadowSm': '0 0 0 1px #000', '--shadowMd': '0 0 0 1px #000', '--shadowLg': '0 0 0 2px #000', '--shadowXl': '0 0 0 2px #000',
	'--buttonBackground': '#ffffff', '--buttonBorderColor': '#000000', '--buttonHoverBackground': '#e6e6e6',
	'--overlayOpaqueBackgroundColor': '#ffffff', '--overlayBackgroundColor': 'rgba(0,0,0,0.5)',
	'--popupColor': '#000000', '--popupBackgroundColor': '#ffffff', '--popupBorderColor': '#000000',
	'--highlightBackground': '#0044cc', '--highlightBorderColor': '#000000', '--highlightBackgroundHover': '#0033aa',
	'--mailboxActiveBackground': '#0044cc', '--mailboxHoverBackgroundColor': '#d6e4ff',
	'--msgItemActiveBackground': '#0044cc', '--msgItemHoverBackgroundColor': '#d6e4ff', '--msgItemFocusBorderColor': '#0044cc',
	'--buttonTristateOnBackground': '#006600', '--buttonTristateOffBackground': '#990000',
	'--warningBackgroundColor': '#ffdd00', '--successBackground': '#006600', '--emphasisBackground': '#000000',
	'--underlineGreen': '#006600', '--underlineRed': '#990000', '--underlineBlue': '#0044cc', '--underlineGrey': '#000000',
	'--quoted1Color': '#000000', '--quoted2Color': '#000000', '--quoted3Color': '#000000',
	'--scriptSwitchUnderlineColor': '#990000',
	'--linkColor': '#0000ee', '--linkVisitedColor': '#551a8b',
	'--accent': '#0044cc', '--accentText': '#ffffff', '--radius': '6px',
	'--radiusSm': '4px', '--radiusMd': '8px', '--radiusLg': '10px', '--radiusFull': '9999px',
	'--easeOut': 'cubic-bezier(0.32, 0.72, 0, 1)',
}
const hcDarkTokens: { [v: string]: string } = {
	'--color': '#ffffff', '--colorMild': '#ffffff', '--colorMilder': '#e6e6e6',
	'--backgroundColor': '#000000', '--backgroundColorMild': '#000000', '--backgroundColorMilder': '#ffffff',
	'--borderColor': '#ffffff', '--mailboxesTopBackgroundColor': '#000000', '--msglistBackgroundColor': '#000000',
	'--boxShadow': '0 0 0 1px #fff', '--shadowSm': '0 0 0 1px #fff', '--shadowMd': '0 0 0 1px #fff', '--shadowLg': '0 0 0 2px #fff', '--shadowXl': '0 0 0 2px #fff',
	'--buttonBackground': '#000000', '--buttonBorderColor': '#ffffff', '--buttonHoverBackground': '#1a1a1a',
	'--overlayOpaqueBackgroundColor': '#000000', '--overlayBackgroundColor': 'rgba(0,0,0,0.7)',
	'--popupColor': '#ffffff', '--popupBackgroundColor': '#000000', '--popupBorderColor': '#ffffff',
	'--highlightBackground': '#66aaff', '--highlightBorderColor': '#ffffff', '--highlightBackgroundHover': '#3388ff',
	'--mailboxActiveBackground': '#0066ff', '--mailboxHoverBackgroundColor': '#003366',
	'--msgItemActiveBackground': '#0066ff', '--msgItemHoverBackgroundColor': '#003366', '--msgItemFocusBorderColor': '#66aaff',
	'--buttonTristateOnBackground': '#00cc00', '--buttonTristateOffBackground': '#ff5555',
	'--warningBackgroundColor': '#ffdd00', '--successBackground': '#00cc00', '--emphasisBackground': '#ffffff',
	'--underlineGreen': '#00ff00', '--underlineRed': '#ff5555', '--underlineBlue': '#66aaff', '--underlineGrey': '#ffffff',
	'--quoted1Color': '#ffffff', '--quoted2Color': '#ffffff', '--quoted3Color': '#ffffff',
	'--scriptSwitchUnderlineColor': '#ffaa00',
	'--linkColor': '#66aaff', '--linkVisitedColor': '#cc99ff',
	'--accent': '#66aaff', '--accentText': '#000000', '--radius': '6px',
	'--radiusSm': '4px', '--radiusMd': '8px', '--radiusLg': '10px', '--radiusFull': '9999px',
	'--easeOut': 'cubic-bezier(0.32, 0.72, 0, 1)',
}

// Forced schemes: a class on <html> overrides the auto (:root + media query)
// tokens. Specificity of html.scheme-* (0,1,1) beats :root (0,1,0).
ensureCSS('html.scheme-light', pickScheme(0))
ensureCSS('html.scheme-dark', pickScheme(1))
ensureCSS('html.scheme-hclight', hcLightTokens)
ensureCSS('html.scheme-hcdark', hcDarkTokens)

// Global typography & antialiasing resets for .theme-modern
ensureCSS('html.theme-modern, .theme-modern body', {
	fontFamily: '-apple-system, BlinkMacSystemFont, "Geist", "Inter", "Segoe UI", Roboto, "Helvetica Neue", sans-serif',
	WebkitFontSmoothing: 'antialiased',
	letterSpacing: '-0.01em',
})
ensureCSS('.theme-modern h1, .theme-modern h2, .theme-modern h3', {
	letterSpacing: '-0.02em',
	fontWeight: '600',
})
// Custom sleek scrollbars for modern theme
ensureCSS('.theme-modern *::-webkit-scrollbar', {
	width: '6px',
	height: '6px',
})
ensureCSS('.theme-modern *::-webkit-scrollbar-track', {
	background: 'transparent',
})
ensureCSS('.theme-modern *::-webkit-scrollbar-thumb', {
	background: 'color-mix(in srgb, var(--colorMild) 25%, transparent)',
	borderRadius: '9999px',
})
ensureCSS('.theme-modern *::-webkit-scrollbar-thumb:hover', {
	background: 'color-mix(in srgb, var(--colorMild) 45%, transparent)',
})

// Touch target minimums and tap highlighting
ensureCSS('.theme-modern button, .theme-modern .button, .theme-modern select, .theme-modern input', {
	WebkitTapHighlightColor: 'transparent',
	touchAction: 'manipulation',
})

// Tactile button styling for .theme-modern
ensureCSS('.theme-modern button, .theme-modern .button', {
	borderRadius: 'var(--radiusSm)',
	border: '1px solid var(--buttonBorderColor)',
	backgroundColor: 'var(--buttonBackground)',
	color: 'var(--color)',
	padding: '.35em .85em',
	fontSize: '.875rem',
	fontWeight: '500',
	lineHeight: '1.25',
	cursor: 'pointer',
	transition: 'transform 150ms cubic-bezier(0.32, 0.72, 0, 1), background-color 150ms cubic-bezier(0.32, 0.72, 0, 1), border-color 150ms cubic-bezier(0.32, 0.72, 0, 1), box-shadow 150ms cubic-bezier(0.32, 0.72, 0, 1)',
	boxShadow: 'var(--shadowSm)',
})
ensureCSS('.theme-modern button:active:not(:disabled), .theme-modern .button:active:not(:disabled)', {
	transform: 'scale(0.97)',
}, true)
ensureCSS('.theme-modern select', {
	borderRadius: 'var(--radiusSm)',
	border: '1px solid var(--buttonBorderColor)',
	backgroundColor: 'var(--buttonBackground)',
	color: 'var(--color)',
	padding: '.35em .75em',
	fontSize: '.875rem',
	fontWeight: '500',
	outline: 'none',
	cursor: 'pointer',
	transition: 'border-color 150ms cubic-bezier(0.32, 0.72, 0, 1), box-shadow 150ms cubic-bezier(0.32, 0.72, 0, 1)',
})
ensureCSS('.theme-modern input, .theme-modern textarea', {
	borderRadius: 'var(--radiusSm)',
	border: '1px solid var(--borderColor)',
	backgroundColor: 'var(--backgroundColor)',
	color: 'var(--color)',
	padding: '.4em .75em',
	fontSize: '.875rem',
	outline: 'none',
	transition: 'border-color 150ms cubic-bezier(0.32, 0.72, 0, 1), box-shadow 150ms cubic-bezier(0.32, 0.72, 0, 1)',
})
ensureCSS('.theme-modern input:focus, .theme-modern textarea:focus, .theme-modern select:focus', {
	borderColor: 'var(--accent)',
	boxShadow: '0 0 0 3px color-mix(in srgb, var(--accent) 20%, transparent)',
}, true)

// Modern top bar with glassmorphism
ensureCSS('.theme-modern .webmailRoot > .topMailboxes', {
	backdropFilter: 'blur(12px)',
	WebkitBackdropFilter: 'blur(12px)',
	backgroundColor: 'var(--mailboxesTopBackgroundColor)',
	borderBottom: '1px solid var(--borderColor)',
	padding: '.4em .6em',
	alignItems: 'center',
	zIndex: '3',
})
ensureCSS('.theme-modern .searchbarElem', {
	borderRadius: 'var(--radiusSm)',
	padding: '.45em .9em',
	fontSize: '.875rem',
	backgroundColor: 'var(--backgroundColorMild)',
	border: '1px solid var(--borderColor)',
	color: 'var(--color)',
	minHeight: '34px',
	transition: 'background-color 150ms cubic-bezier(0.32, 0.72, 0, 1), border-color 150ms cubic-bezier(0.32, 0.72, 0, 1), box-shadow 150ms cubic-bezier(0.32, 0.72, 0, 1)',
})
ensureCSS('.theme-modern .composeButton', {
	background: 'var(--accent)',
	color: 'var(--accentText)',
	borderColor: 'transparent',
	borderRadius: 'var(--radiusSm)',
	padding: '.45em 1.25em',
	fontSize: '.875rem',
	fontWeight: '600',
	letterSpacing: '-0.01em',
	minHeight: '34px',
	boxShadow: 'inset 0 1px 0 rgba(255, 255, 255, 0.2), var(--shadowSm)',
	transition: 'transform 150ms cubic-bezier(0.32, 0.72, 0, 1), background-color 150ms cubic-bezier(0.32, 0.72, 0, 1), box-shadow 150ms cubic-bezier(0.32, 0.72, 0, 1)',
}, true)

// Modern folder sidebar
ensureCSS('.theme-modern .mailboxesBox', {
	backgroundColor: 'var(--backgroundColorMild)',
	borderRight: '1px solid var(--borderColor)',
})
ensureCSS('.theme-modern .mailboxItem', {
	borderRadius: 'var(--radiusSm)',
	margin: '1px 4px',
	padding: '.45em .75em',
	fontSize: '.875rem',
	fontWeight: '500',
	display: 'flex',
	alignItems: 'center',
	border: '1px solid transparent',
	transition: 'transform 150ms cubic-bezier(0.32, 0.72, 0, 1), background-color 150ms cubic-bezier(0.32, 0.72, 0, 1), color 150ms cubic-bezier(0.32, 0.72, 0, 1), border-color 150ms cubic-bezier(0.32, 0.72, 0, 1)',
})
ensureCSS('.theme-modern .mailboxItem:active', {
	transform: 'scale(0.98)',
})
ensureCSS('.theme-modern .mailboxItem.active', {
	background: 'var(--mailboxActiveBackground)',
	color: 'var(--color)',
	fontWeight: '600',
	borderColor: 'color-mix(in srgb, var(--accent) 15%, transparent)',
}, true)
ensureCSS('.theme-modern .mailboxIcon', {
	display: 'inline-flex',
	alignItems: 'center',
	justifyContent: 'center',
	width: '1.4em',
	marginRight: '.5em',
	fontSize: '1rem',
	flex: 'none',
})
ensureCSS('.theme-modern .mailboxUnread:not(:empty)', {
	background: 'var(--accent)',
	color: 'var(--accentText)',
	borderRadius: 'var(--radiusFull)',
	padding: '0.1em .55em',
	fontSize: '.75rem',
	fontWeight: '600',
	letterSpacing: '0',
	marginLeft: 'auto',
	boxShadow: 'var(--shadowSm)',
})

// Modern message list: precision 2-line layout
ensureCSS('.theme-modern .msgItem', {
	display: 'grid',
	gridTemplateColumns: 'auto auto minmax(0, 1fr) auto',
	gridTemplateAreas: '"flags avatar from age" "flags avatar subject subject"',
	columnGap: '.65em',
	rowGap: '.15em',
	alignItems: 'center',
	padding: '.6em .85em',
	margin: '2px 4px',
	border: '1px solid transparent',
	borderRadius: 'var(--radiusMd)',
	position: 'relative',
	fontSize: 'calc(.875rem * var(--ml-scale, 1))',
	lineHeight: '1.35',
	transition: 'transform 150ms cubic-bezier(0.32, 0.72, 0, 1), background-color 150ms cubic-bezier(0.32, 0.72, 0, 1), border-color 150ms cubic-bezier(0.32, 0.72, 0, 1), box-shadow 150ms cubic-bezier(0.32, 0.72, 0, 1)',
})
ensureCSS('.theme-modern .msgItem:active', {
	transform: 'scale(0.985)',
})
ensureCSS('.theme-modern .msgItemCell', {padding: 0, width: 'auto'})
ensureCSS('.theme-modern .msgItemFlags', {gridArea: 'flags', display: 'flex', alignItems: 'center', padding: 0, width: 'auto'})
ensureCSS('.theme-modern .msgItemFlag', {display: 'none'})
ensureCSS('.theme-modern .msgItemFrom', {gridArea: 'from', width: 'auto', position: 'static'})
ensureCSS('.theme-modern .msgItemFromText', {
	fontWeight: 'var(--ml-from-weight, 600)',
	fontStyle: 'var(--ml-from-style, normal)',
	color: 'var(--color)',
	letterSpacing: '-0.01em',
})
ensureCSS('.theme-modern .msgItemThreadBar', {
	left: '.6em',
	right: 'auto',
	borderLeft: '1.5px solid var(--colorMilder)',
	borderRight: 'none',
})
ensureCSS('.theme-modern .msgItemThreadBarMiddle', {top: '-.6em', bottom: '-.6em'})
ensureCSS('.theme-modern .msgItemThreadBarFirst', {top: '40%', bottom: '-.6em'})
ensureCSS('.theme-modern .msgItemThreadBarLast', {top: '-.6em', bottom: '50%'})
ensureCSS('.theme-modern .msgItemSubject', {gridArea: 'subject', width: 'auto'})
ensureCSS('.theme-modern .msgItemAge', {
	gridArea: 'age',
	width: 'auto',
	color: 'var(--colorMild)',
	fontSize: '.8rem',
	fontWeight: 'var(--ml-date-weight, 500)',
	fontStyle: 'var(--ml-date-style, normal)',
	letterSpacing: '0',
})
ensureCSS('.theme-modern .msgItemAvatar', {
	display: 'flex',
	gridArea: 'avatar',
	alignItems: 'center',
	justifyContent: 'center',
	width: '36px',
	height: '36px',
	borderRadius: 'var(--radiusFull)',
	color: '#ffffff',
	fontSize: '.8rem',
	fontWeight: '600',
	alignSelf: 'center',
	boxShadow: 'inset 0 1px 0 rgba(255, 255, 255, 0.2), var(--shadowSm)',
})
ensureCSS('.theme-modern .msgItemSubjectText', {
	display: '-webkit-box',
	WebkitBoxOrient: 'vertical',
	WebkitLineClamp: '2',
	overflow: 'hidden',
	whiteSpace: 'normal',
	color: 'var(--colorMild)',
})
ensureCSS('.theme-modern .msgItemSubjectTitle', {
	display: 'inline',
	color: 'var(--color)',
	fontWeight: 'var(--ml-subj-weight, 500)',
	fontStyle: 'var(--ml-subj-style, normal)',
	letterSpacing: '-0.01em',
})
ensureCSS('.theme-modern .msgItemSubjectSnippet', {
	display: 'inline',
	margin: 0,
	color: 'var(--colorMild)',
	fontWeight: 'var(--ml-prev-weight, 400)',
	fontStyle: 'var(--ml-prev-style, normal)',
})

// Unread emphasis states in modern list
ensureCSS('.theme-modern.unread-bar .msgItem.msgItemUnread, .theme-modern.unread-barbold .msgItem.msgItemUnread', {
	boxShadow: 'inset 3px 0 0 var(--accent)',
})
ensureCSS('.theme-modern.unread-bold .msgItem.msgItemUnread .msgItemFromText, .theme-modern.unread-bold .msgItem.msgItemUnread .msgItemSubjectTitle, .theme-modern.unread-barbold .msgItem.msgItemUnread .msgItemFromText, .theme-modern.unread-barbold .msgItem.msgItemUnread .msgItemSubjectTitle', {
	fontWeight: '700',
	color: 'var(--color)',
})
ensureCSS('.theme-modern.unread-tint .msgItem.msgItemUnread', {
	background: 'color-mix(in srgb, var(--accent) 8%, transparent)',
})
ensureCSS('.theme-modern.unread-dot .msgItem.msgItemUnread::after', {
	content: '""',
	position: 'absolute',
	left: '.3em',
	top: '50%',
	transform: 'translateY(-50%)',
	width: '6px',
	height: '6px',
	borderRadius: '50%',
	background: 'var(--accent)',
	boxShadow: '0 0 6px var(--accent)',
})

// Selection and hover states
ensureCSS('.theme-modern .msgItem.active', {
	background: 'var(--msgItemActiveBackground)',
	borderColor: 'color-mix(in srgb, var(--accent) 20%, transparent)',
	boxShadow: 'var(--shadowSm)',
}, true)

// Modern message list filter toolbar
ensureCSS('.theme-modern .refineTitle', {display: 'none'})
ensureCSS('.theme-modern .msgListFilterSorting', {
	padding: '.4em .75em',
	gap: '.5em',
	alignItems: 'center',
	borderBottom: '1px solid var(--borderColor)',
	backgroundColor: 'var(--backgroundColorMild)',
})
ensureCSS('.theme-modern .msgListFilterSorting > div', {display: 'flex', alignItems: 'center', gap: '.4em', flexWrap: 'nowrap'})
ensureCSS('.theme-modern .msgListFilterSorting .btngroup', {display: 'inline-flex', alignItems: 'center', gap: '.25em'})
ensureCSS('.theme-modern .msgListFilterSorting button', {
	borderRadius: 'var(--radiusSm)',
	padding: '.3em .75em',
	fontSize: '.8125rem',
	fontWeight: '500',
	lineHeight: '1.2',
	border: '1px solid var(--buttonBorderColor)',
	boxShadow: 'none',
}, true)
ensureCSS('.theme-modern .msgListFilterSorting select', {
	borderRadius: 'var(--radiusSm)',
	fontSize: '.8125rem',
	padding: '.25em .6em',
	border: '1px solid var(--buttonBorderColor)',
})
ensureCSS('.theme-modern .msgListFilterSorting button.active, .theme-modern .msgListFilterSorting button.invert', {
	background: 'var(--accent)',
	color: 'var(--accentText)',
	borderColor: 'transparent',
	boxShadow: 'var(--shadowSm)',
}, true)
ensureCSS('.theme-modern .msgListFilterSorting .refineIcon', {
	fontSize: '0',
	width: '28px',
	height: '28px',
	padding: '0',
	borderRadius: 'var(--radiusSm)',
	display: 'inline-flex',
	alignItems: 'center',
	justifyContent: 'center',
	background: 'transparent',
	border: '1px solid transparent',
	boxShadow: 'none',
	transition: 'transform 150ms cubic-bezier(0.32, 0.72, 0, 1), background-color 150ms cubic-bezier(0.32, 0.72, 0, 1)',
}, true)
ensureCSS('.theme-modern .refineUnread::before', {content: '"\u{1F4E9}"', fontSize: '.9rem', lineHeight: '1'})
ensureCSS('.theme-modern .refineRead::before', {content: '"\u{1F4D6}"', fontSize: '.9rem', lineHeight: '1'})
ensureCSS('.theme-modern .refineAttachments::before', {content: '"\u{1F4CE}"', fontSize: '.9rem', lineHeight: '1'})
ensureCSS('.theme-modern .refineClear::before', {content: '"✕"', fontSize: '.85rem', lineHeight: '1', fontWeight: 'bold'})
ensureCSS('.theme-modern .refineLabel::before', {content: '"\u{1F3F7}"', fontSize: '.9rem', lineHeight: '1'})

// Keywords / labels chips
ensureCSS('.theme-modern .keyword', {
	padding: '.15em .6em',
	borderRadius: 'var(--radiusFull)',
	fontSize: '.75rem',
	fontWeight: '500',
	margin: '0 .2em',
	background: 'color-mix(in srgb, var(--accent) 12%, transparent)',
	color: 'var(--color)',
	border: '1px solid color-mix(in srgb, var(--accent) 25%, transparent)',
	letterSpacing: '0',
})

// Modern reading pane: reordered flex column
ensureCSS('.theme-modern .msgmeta', {
	display: 'flex',
	flexDirection: 'column',
	padding: '1em 1.25em',
	gap: '.35em',
	backgroundColor: 'var(--backgroundColorMild)',
	borderBottom: '1px solid var(--borderColor)',
})
ensureCSS('.theme-modern .msgModernSubject', {
	display: 'block',
	order: '1',
	margin: '.1em 0 .3em',
	fontSize: 'calc(1.35rem * var(--mv-subj-scale, 1))',
	fontWeight: '600',
	letterSpacing: '-0.02em',
	lineHeight: '1.3',
	color: 'var(--color)',
})
ensureCSS('.theme-modern .msgModernSender', {
	display: 'flex',
	order: '2',
	alignItems: 'center',
	gap: '.75em',
	marginBottom: '.4em',
})
ensureCSS('.theme-modern .msgButtons', {order: '3'})
ensureCSS('.theme-modern .msgDetails', {order: '4'})
ensureCSS('.theme-modern .headerBodySeparator', {order: '5', height: '1px', backgroundColor: 'var(--borderColor)'})

// Reading pane avatar & sender identity
ensureCSS('.theme-modern .msgModernAvatar', {
	display: 'flex',
	alignItems: 'center',
	justifyContent: 'center',
	flex: 'none',
	width: '42px',
	height: '42px',
	borderRadius: 'var(--radiusFull)',
	color: '#ffffff',
	fontSize: '.95rem',
	fontWeight: '600',
	boxShadow: 'inset 0 1px 0 rgba(255, 255, 255, 0.2), var(--shadowSm)',
})
ensureCSS('.theme-modern .msgModernSenderText', {display: 'flex', flexDirection: 'column', minWidth: '0', gap: '2px'})
ensureCSS('.theme-modern .msgModernSenderName', {
	fontWeight: 'var(--mv-sender-weight, 600)',
	fontStyle: 'var(--mv-sender-style, normal)',
	fontSize: '.95rem',
	color: 'var(--color)',
	letterSpacing: '-0.01em',
	overflow: 'hidden',
	textOverflow: 'ellipsis',
	whiteSpace: 'nowrap',
})
ensureCSS('.theme-modern .msgModernSenderTime', {color: 'var(--colorMild)', fontSize: '.8125rem', letterSpacing: '0'})
ensureCSS('.theme-modern .msgModernSenderActions', {marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: '.5em', flex: 'none'})
ensureCSS('.theme-modern .msgModernSenderActions button', {
	borderRadius: 'var(--radiusSm)',
	padding: '.25em .75em',
	fontSize: '.8125rem',
}, true)
ensureCSS('.theme-modern .msgMode', {display: 'none'})
ensureCSS('.theme-modern .msgmeta .msgHeaders', {display: 'none'})
ensureCSS('.theme-modern .msgmeta.detailsExpanded .msgHeaders', {
	display: 'table',
	marginTop: '.5em',
	padding: '.5em',
	backgroundColor: 'var(--backgroundColor)',
	borderRadius: 'var(--radiusSm)',
	border: '1px solid var(--borderColor)',
})

// Reading pane action buttons
ensureCSS('.theme-modern .msgmeta .msgButtons', {padding: '.2em 0'})
ensureCSS('.theme-modern .msgmeta .msgButtons button', {
	borderRadius: 'var(--radiusSm)',
	padding: '.35em .9em',
	fontSize: '.8125rem',
	fontWeight: '500',
	marginRight: '.35em',
	marginBottom: '.35em',
}, true)
ensureCSS('.theme-modern .msgmeta .msgReplyButton', {
	background: 'var(--accent)',
	color: 'var(--accentText)',
	borderColor: 'transparent',
	fontWeight: '600',
	boxShadow: 'inset 0 1px 0 rgba(255, 255, 255, 0.2), var(--shadowSm)',
}, true)

// Message body container
ensureCSS('.theme-modern .msgscroll', {
	padding: '1.25em 1.5em',
	backgroundColor: 'var(--backgroundColor)',
	lineHeight: '1.6',
	fontSize: '.9375rem',
})

// Floating Compose Modal overhaul
ensureCSS('.theme-modern .composePopup', {
	borderRadius: 'var(--radiusLg)',
	border: '1px solid var(--borderColor)',
	backgroundColor: 'var(--popupBackgroundColor)',
	backdropFilter: 'blur(16px)',
	WebkitBackdropFilter: 'blur(16px)',
	boxShadow: 'var(--shadowXl)',
	padding: '1.2em',
	transformOrigin: 'bottom right',
})

// Popups and dropdown modals origin-aware animation
ensureCSS('.theme-modern .popup, .theme-modern .popover', {
	borderRadius: 'var(--radiusMd)',
	border: '1px solid var(--popupBorderColor)',
	backgroundColor: 'var(--popupBackgroundColor)',
	backdropFilter: 'blur(12px)',
	WebkitBackdropFilter: 'blur(12px)',
	boxShadow: 'var(--shadowLg)',
	padding: '.75em',
	transformOrigin: 'top center',
})

// Typed way to reference a css variables. Kept from before used variables.
export const styles = {
	color: 'var(--color)',
	colorMild: 'var(--colorMild)',
	colorMilder: 'var(--colorMilder)',
	backgroundColor: 'var(--backgroundColor)',
	backgroundColorMild: 'var(--backgroundColorMild)',
	backgroundColorMilder: 'var(--backgroundColorMilder)',
	borderColor: 'var(--borderColor)',
	mailboxesTopBackgroundColor: 'var(--mailboxesTopBackgroundColor)',
	msglistBackgroundColor: 'var(--msglistBackgroundColor)',
	boxShadow: 'var(--boxShadow)',

	buttonBackground: 'var(--buttonBackground)',
	buttonBorderColor: 'var(--buttonBorderColor)',
	buttonHoverBackground: 'var(--buttonHoverBackground)',

	overlayOpaqueBackgroundColor: 'var(--overlayOpaqueBackgroundColor)',
	overlayBackgroundColor: 'var(--overlayBackgroundColor)',

	popupColor: 'var(--popupColor)',
	popupBackgroundColor: 'var(--popupBackgroundColor)',
	popupBorderColor: 'var(--popupBorderColor)',

	highlightBackground: 'var(--highlightBackground)',
	highlightBorderColor: 'var(--highlightBorderColor)',
	highlightBackgroundHover: 'var(--highlightBackgroundHover)',

	mailboxActiveBackground: 'var(--mailboxActiveBackground)',
	mailboxHoverBackgroundColor: 'var(--mailboxHoverBackgroundColor)',

	msgItemActiveBackground: 'var(--msgItemActiveBackground)',
	msgItemHoverBackgroundColor: 'var(--msgItemHoverBackgroundColor)',
	msgItemFocusBorderColor: 'var(--msgItemFocusBorderColor)',

	buttonTristateOnBackground: 'var(--buttonTristateOnBackground)',
	buttonTristateOffBackground: 'var(--buttonTristateOffBackground)',

	warningBackgroundColor: 'var(--warningBackgroundColor)',

	successBackground: 'var(--successBackground)',
	emphasisBackground: 'var(--emphasisBackground)',

	// For authentication/security results.
	underlineGreen: 'var(--underlineGreen)',
	underlineRed: 'var(--underlineRed)',
	underlineBlue: 'var(--underlineBlue)',
	underlineGrey: 'var(--underlineGrey)',

	quoted1Color: 'var(--quoted1Color)',
	quoted2Color: 'var(--quoted2Color)',
	quoted3Color: 'var(--quoted3Color)',

	scriptSwitchUnderlineColor: 'var(--scriptSwitchUnderlineColor)',

	linkColor: 'var(--linkColor)',
	linkVisitedColor: 'var(--linkVisitedColor)',
}
export const styleClasses = {
	// For quoted text, with multiple levels of indentations.
	quoted: [
		css('quoted1', {color: styles.quoted1Color}),
		css('quoted2', {color: styles.quoted2Color}),
		css('quoted3', {color: styles.quoted3Color}),
	],
	// When text switches between unicode scripts.
	scriptswitch: css('scriptswitch', {textDecoration: 'underline 2px', textDecorationColor: styles.scriptSwitchUnderlineColor}),
	textMild: css('textMild', {color: styles.colorMild}),
	// For keywords (also known as flags/labels/tags) on messages.
	keyword: css('keyword', {padding: '0 .15em', borderRadius: '.15em', fontWeight: 'normal', fontSize: '.9em', margin: '0 .15em', whiteSpace: 'nowrap', background: styles.highlightBackground, color: styles.color, border: '1px solid', borderColor: styles.highlightBorderColor}),
	msgHeaders: css('msgHeaders', {marginBottom: '1ex', width: '100%'}),
}

ensureCSS('.msgHeaders td', {wordBreak: 'break-word'}) // Prevent horizontal scroll bar for long header values.
ensureCSS('.keyword.keywordCollapsed', {opacity: .75}),

// Generic styling.
ensureCSS('html', {backgroundColor: 'var(--backgroundColor)', color: 'var(--color)'})
ensureCSS('*', {fontSize: 'inherit', fontFamily: "'ubuntu', 'lato', sans-serif", margin: 0, padding: 0, boxSizing: 'border-box'})
ensureCSS('.mono, .mono *', {fontFamily: "'ubuntu mono', monospace"})
ensureCSS('table td, table th', {padding: '.15em .25em'})
ensureCSS('.pad', {padding: '.5em'})
ensureCSS('iframe', {border: 0})
ensureCSS('img, embed, video, iframe', {backgroundColor: 'white', color: 'black'})
ensureCSS('a', {color: styles.linkColor})
ensureCSS('a:visited', {color: styles.linkVisitedColor})

// For message view with multiple inline elements (often a single text and multiple messages).
ensureCSS('.textmulti > *:nth-child(even)', {backgroundColor: ['#f4f4f4', '#141414']})
ensureCSS('.textmulti > *', {padding: '2ex .5em', margin: '-.5em' /* compensate pad */ })
ensureCSS('.textmulti > *:first-child', {padding: '.5em'})


// join elements in l with the results of calls to efn. efn can return
// HTMLElements, which cannot be inserted into the dom multiple times, hence the
// function.
export const join = (l: any, efn: () => any): any[] => {
	const r: any[] = []
	const n = l.length
	for (let i = 0; i < n; i++) {
		r.push(l[i])
		if (i < n-1) {
			r.push(efn())
		}
	}
	return r
}

// From https://developer.mozilla.org/en-US/docs/Web/Media/Formats/Image_types
const imageTypes = [
	'image/avif',
	'image/webp',
	'image/gif',
	'image/png',
	'image/jpeg',
	'image/apng',
	'image/svg+xml',
]
export const isImage = (a: api.Attachment) => imageTypes.includes((a.Part.MediaType + '/' + a.Part.MediaSubType).toLowerCase())

// addLinks turns a line of text into alternating strings and links. Links that
// would end with interpunction followed by whitespace are returned with that
// interpunction moved to the next string instead.
const addLinks = (text: string): (HTMLAnchorElement | string)[] => {
	// todo: look at ../rfc/3986 and fix up regexp. we should probably accept utf-8.
	const re = RegExp('(?:(http|https):\/\/|mailto:)([:%0-9a-zA-Z._~!$&\'/()*+,;=-]+@)?([\\[\\]0-9a-zA-Z.-]+)(:[0-9]+)?([:@%0-9a-zA-Z._~!$&\'/()*+,;=-]*)(\\?[:@%0-9a-zA-Z._~!$&\'/()*+,;=?-]*)?(#[:@%0-9a-zA-Z._~!$&\'/()*+,;=?-]*)?')
	const r = []
	while (text.length > 0) {
		const l = re.exec(text)
		if (!l) {
			r.push(text)
			break
		}
		let s = text.substring(0, l.index)
		let url = l[0]
		text = text.substring(l.index+url.length)
		r.push(s)
		// If URL ends with interpunction, and next character is whitespace or end, don't
		// include the interpunction in the URL.
		if (!text || /^[ \t\r\n]/.test(text)) {
			if (/[)>][!,.:;?]$/.test(url)) {
				text = url.substring(url.length-2)+text
				url = url.substring(0, url.length-2)
			} else if (/[)>!,.:;?]$/.test(url)) {
				text = url.substring(url.length-1)+text
				url = url.substring(0, url.length-1)
			}
		}
		r.push(dom.a(url, attr.href(url), url.startsWith('mailto:') ? [] : [attr.target('_blank'), attr.rel('noopener noreferrer')]))
	}
	return r
}

// renderText turns text into a renderable element with ">" interpreted as quoted
// text (with different levels), and URLs replaced by links.
export const renderText = (text: string): HTMLElement => {
	return dom.div(text.split('\n').map(line => {
		let q = 0
		for (const c of line) {
			if (c == '>') {
				q++
			} else if (c !== ' ') {
				break
			}
		}

		if (q == 0) {
			return [addLinks(line), '\n']
		}
		return dom.div(styleClasses.quoted[q%styleClasses.quoted.length], addLinks(line))
	}))
}

export const displayName = (s: string) => {
	// ../rfc/5322:1216
	// ../rfc/5322:1270
	// todo: need support for group addresses (eg "undisclosed recipients").
	// ../rfc/5322:697
	const specials = /[()<>\[\]:;@\\,."]/
	if (specials.test(s)) {
		return '"' + s.replace('\\', '\\\\').replace('"', '\\"') + '"'
	}
	return s
}

export const formatDomain = (dom: api.Domain) => dom.Unicode || dom.ASCII

// format an address with both name and email address.
export const formatAddress = (a: api.MessageAddress): string => {
	let s = '<' + a.User + '@' + formatDomain(a.Domain) + '>'
	if (a.Name) {
		s = displayName(a.Name) + ' ' + s
	}
	return s
}

// Like formatAddress, but returns an element with a title (for hover) with the ASCII domain, in case of IDN.
export const formatAddressElem = (a: api.MessageAddress): string | HTMLElement => {
	if (!a.Domain.Unicode) {
		return formatAddress(a)
	}
	return dom.span(a.Name ? [displayName(a.Name), ' '] : '', '<', a.User, '@', dom.span(attr.title(a.Domain.ASCII), formatDomain(a.Domain)), '>')
}

// like formatAddress, but underline domain with dmarc-like validation if appropriate.
export const formatAddressValidated = (a: api.MessageAddress, m: api.Message, use: boolean): (string | HTMLElement)[] => {
	const domainText = (domstr: string, ascii: string): HTMLElement | string => {
		if (!use) {
			return domstr
		}
		const extra = domstr === ascii ? '' : '; domain '+ascii
		// We want to show how "approved" this message is given the message From's domain.
		// We have MsgFromValidation available. It's not the greatest, being a mix of
		// potential strict validations, actual DMARC policy validation, potential relaxed
		// validation, but no explicit fail or (temporary) errors. We also don't know if
		// historic messages were from a mailing list. We could add a heuristic based on
		// List-Id headers, but it would be unreliable...
		// todo: add field to Message with the exact results.
		let name = ''
		let color = ''
		let title = ''
		switch (m.MsgFromValidation) {
		case api.Validation.ValidationStrict:
			name = 'Strict'
			color = styles.underlineGreen
			title = 'Message would have matched a strict DMARC policy.'
			break
		case api.Validation.ValidationDMARC:
			name = 'DMARC'
			color = styles.underlineGreen
			title = 'Message matched DMARC policy of domain.'
			break
		case api.Validation.ValidationRelaxed:
			name = 'Relaxed'
			color = styles.underlineGreen
			title = 'Domain did not have a DMARC policy, but message would match a relaxed policy if it had existed.'
			break;
		case api.Validation.ValidationNone:
			if (m.IsForward || m.IsMailingList) {
				name = 'Forwardlist'
				color = styles.underlineBlue
				title = 'Message would not pass DMARC policy, but came in through a configured mailing list or forwarding address.'
			} else {
				name = 'Bad'
				color = styles.underlineRed
				title = 'Either domain did not have a DMARC policy, or message did not adhere to it.'
			}
			break;
		default:
			// Also for zero value, when unknown. E.g. for sent messages added with IMAP.
			name = 'Unknown'
			title = 'Unknown DMARC verification result.'
			return dom.span(attr.title(title+extra), domstr)
		}
		return dom.span(attr.title(title+extra), css('addressValidation'+name, {borderBottom: '1.5px solid', borderBottomColor: color, textDecoration: 'none'}), domstr)
	}

	let l: (string | HTMLElement)[] = []
	if (a.Name) {
		l.push(a.Name + ' ')
	}
	l.push('<' + a.User + '@')
	l.push(domainText(formatDomain(a.Domain), a.Domain.ASCII))
	l.push('>')
	return l
}

// format just the name if present and it doesn't look like an address, or otherwise just the email address.
export const formatAddressShort = (a: api.MessageAddress, junk: boolean): string => {
	const n = a.Name
	if (!junk && n && !n.includes('<') && !n.includes('@') && !n.includes('>')) {
		return n
	}
	return '<' + a.User + '@' + formatDomain(a.Domain) + '>'
}

// return just the email address.
export const formatEmail = (a: api.MessageAddress) => a.User + '@' + formatDomain(a.Domain)

export const equalAddress = (a: api.MessageAddress, b: api.MessageAddress) => {
	return (!a.User || !b.User || a.User === b.User) && a.Domain.ASCII === b.Domain.ASCII
}

const addressList = (allAddrs: boolean, l: api.MessageAddress[]) => {
	if (l.length <= 5 || allAddrs) {
		return dom.span(join(l.map(a => formatAddressElem(a)), () => ', '))
	}
	let elem = dom.span(
		join(
			l.slice(0, 4).map(a => formatAddressElem(a)),
			() => ', '
		),
		' ',
		dom.clickbutton('More...', attr.title('More addresses:\n'+l.slice(4).map(a => formatAddress(a)).join(',\n')), function click() {
			const nelem = dom.span(
				join(l.map(a => formatAddressElem(a)), () => ', '),
				' ',
				dom.clickbutton('Less...', function click() {
					elem.replaceWith(addressList(allAddrs, l))
				}),
			)
			elem.replaceWith(nelem)
			elem = nelem
		})
	)
	return elem
}

// loadMsgheaderView loads the common message headers into msgheaderelem.
// if refineKeyword is set, labels are shown and a click causes a call to
// refineKeyword.
export const loadMsgheaderView = (msgheaderelem: HTMLTableSectionElement, mi: api.MessageItem, moreHeaders: string[], refineKeyword: null | ((kw: string) => Promise<void>), allAddrs: boolean) => {
	const msgenv = mi.Envelope
	const received = mi.Message.Received
	const receivedlocal = new Date(received.getTime())
	// Similar to webmail.ts:/headerTextMildStyle
	const msgHeaderFieldStyle = css('msgHeaderField', {textAlign: 'right', color: styles.colorMild, whiteSpace: 'nowrap'})
	const msgAttrStyle = css('msgAttr', {padding: '0px 0.15em', fontSize: '.9em'})
	dom._kids(msgheaderelem,
		// todo: make addresses clickable, start search (keep current mailbox if any)
		dom.tr(
			dom.td('From:', msgHeaderFieldStyle),
			dom.td(
				style({width: '100%'}),
				dom.div(css('msgFromReceivedSpread', {display: 'flex', justifyContent: 'space-between'}),
					dom.div(join((msgenv.From || []).map(a => formatAddressValidated(a, mi.Message, !!msgenv.From && msgenv.From.length === 1)), () => ', ')),
					dom.div(
						attr.title('Received: ' + received.toString() + ';\nDate header in message: ' + (msgenv.Date ? msgenv.Date.toString() : '(missing/invalid)')),
						receivedlocal.toDateString() + ' ' + receivedlocal.toTimeString().split(' ')[0],
					),
				)
			),
		),
		(msgenv.ReplyTo || []).length === 0 ? [] : dom.tr(
			dom.td('Reply-To:', msgHeaderFieldStyle),
			dom.td(join((msgenv.ReplyTo || []).map(a => formatAddressElem(a)), () => ', ')),
		),
		dom.tr(
			dom.td('To:', msgHeaderFieldStyle),
			dom.td(addressList(allAddrs, msgenv.To || [])),
		),
		(msgenv.CC || []).length === 0 ? [] : dom.tr(
			dom.td('Cc:', msgHeaderFieldStyle),
			dom.td(addressList(allAddrs, msgenv.CC || [])),
		),
		(msgenv.BCC || []).length === 0 ? [] : dom.tr(
			dom.td('Bcc:', msgHeaderFieldStyle),
			dom.td(addressList(allAddrs, msgenv.BCC || [])),
		),
		dom.tr(
			dom.td('Subject:', msgHeaderFieldStyle),
			dom.td(
				dom.div(css('msgSubjectAttrsSpread', {display: 'flex', justifyContent: 'space-between'}),
					dom.div(msgenv.Subject || ''),
					dom.div(
						mi.Message.IsForward ? dom.span(msgAttrStyle, 'Forwarded', attr.title('Message came in from a forwarded address. Some message authentication policies, like DMARC, were not evaluated.')) : [],
						mi.Message.IsMailingList ? dom.span(msgAttrStyle, 'Mailing list', attr.title('Message was received from a mailing list. Some message authentication policies, like DMARC, were not evaluated.')) : [],
						mi.Message.ReceivedTLSVersion === 1 ? dom.span(msgAttrStyle, css('msgAttrNoTLS', {borderBottom: '1.5px solid', borderBottomColor: styles.underlineRed}), 'Without TLS', attr.title('Message received (last hop) without TLS.')) : [],
						mi.Message.ReceivedTLSVersion > 1 && !mi.Message.ReceivedRequireTLS ? dom.span(msgAttrStyle, css('msgAttrTLS', {borderBottom: '1.5px solid', borderBottomColor: styles.underlineGreen}), 'With TLS', attr.title('Message received (last hop) with TLS.')) : [],
						mi.Message.ReceivedRequireTLS ? dom.span(css('msgAttrRequireTLS', {padding: '.1em .3em', fontSize: '.9em', backgroundColor: styles.successBackground, border: '1px solid', borderColor: styles.borderColor, borderRadius: '3px'}), 'With RequireTLS', attr.title('Transported with RequireTLS, ensuring TLS along the entire delivery path from sender to recipient, with TLS certificate verification through MTA-STS and/or DANE.')) : [],
						mi.IsSigned ? dom.span(msgAttrStyle, css('msgAttrSigned', {backgroundColor: styles.colorMild, color: styles.backgroundColorMild, borderRadius: '.15em'}), 'Message has a signature') : [],
						mi.IsEncrypted ? dom.span(msgAttrStyle, css('msgAttrEncrypted', {backgroundColor: styles.colorMild, color: styles.backgroundColorMild, borderRadius: '.15em'}), 'Message is encrypted') : [],
						refineKeyword ? (mi.Message.Keywords || []).map(kw =>
							dom.clickbutton(styleClasses.keyword, dom._class('keywordButton'), kw, async function click() {
								await refineKeyword(kw)
							}),
						) : [],
					),
				)
			),
		),
		(mi.MoreHeaders || []).map(t =>
			dom.tr(
				dom.td(t![0]+':', msgHeaderFieldStyle),
				dom.td(t![1]),
			),
		),
		// Ensure width of all possible additional headers is taken into account, to
		// prevent different layout between messages when not all headers are present.
		dom.tr(
			dom.td(moreHeaders.map(s => dom.div(s+':', msgHeaderFieldStyle, style({visibility: 'hidden', height: 0})))),
			dom.td(),
		),
	)
}
