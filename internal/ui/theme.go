// Package ui is the Fyne desktop interface of Simpel Playtime.
package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// Palette of the dark purple theme. Everything visual pulls from here.
var (
	colBackground  = rgb(0x0e, 0x0b, 0x14)
	colSidebar     = rgb(0x13, 0x0f, 0x1b)
	colSurface     = rgb(0x1c, 0x17, 0x27) // cards, chart surface
	colSurface2    = rgb(0x24, 0x1d, 0x33) // inputs, raised bits
	colBorder      = rgb(0x2e, 0x26, 0x40)
	colPrimary     = rgb(0xa5, 0x6b, 0xff)
	colPrimarySoft = rgba(0xa5, 0x6b, 0xff, 0x33)
	colPrimaryLite = rgb(0xc9, 0xa8, 0xff)
	colText        = rgb(0xec, 0xe8, 0xf4)
	colTextDim     = rgb(0xa5, 0x9c, 0xb8)
	colTextMuted   = rgb(0x6f, 0x66, 0x84)
	colGood        = rgb(0x0c, 0xa3, 0x0c)
	colGoodSoft    = rgba(0x0c, 0xa3, 0x0c, 0x30)
	colWarn        = rgb(0xfa, 0xb2, 0x19)
	colBad         = rgb(0xd0, 0x3b, 0x3b)
	colStar        = rgb(0xf5, 0xc5, 0x42)
	colEmptyCell   = rgb(0x24, 0x1d, 0x33)
)

// series colours for stacked charts, validated for the dark surface
// (categorical order, never cycled; anything past five folds into "Other")
var seriesColors = []color.NRGBA{
	rgb(0x39, 0x87, 0xe5),
	rgb(0xd9, 0x59, 0x26),
	rgb(0x19, 0x9e, 0x70),
	rgb(0xc9, 0x85, 0x00),
	rgb(0xd5, 0x51, 0x81),
}

var colOther = rgb(0x6f, 0x66, 0x84)

// heat ramp: one purple hue, dark (little) to bright (a lot)
var heatRamp = []color.NRGBA{
	rgb(0x3b, 0x2d, 0x5c),
	rgb(0x5a, 0x3f, 0x94),
	rgb(0x7b, 0x52, 0xcc),
	rgb(0xa5, 0x6b, 0xff),
	rgb(0xc9, 0xa8, 0xff),
}

func rgb(r, g, b uint8) color.NRGBA                { return color.NRGBA{R: r, G: g, B: b, A: 0xff} }
func rgba(r, g, b, a uint8) color.NRGBA            { return color.NRGBA{R: r, G: g, B: b, A: a} }
func withAlpha(c color.NRGBA, a uint8) color.NRGBA { c.A = a; return c }

type purpleTheme struct{}

var _ fyne.Theme = purpleTheme{}

func (purpleTheme) Color(n fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	switch n {
	case theme.ColorNameBackground:
		return colBackground
	case theme.ColorNameButton:
		return colSurface2
	case theme.ColorNameDisabledButton:
		return colSurface
	case theme.ColorNameDisabled:
		return colTextMuted
	case theme.ColorNameError:
		return colBad
	case theme.ColorNameFocus:
		return withAlpha(colPrimary, 0x80)
	case theme.ColorNameForeground:
		return colText
	case theme.ColorNameForegroundOnPrimary:
		return color.White
	case theme.ColorNameForegroundOnError, theme.ColorNameForegroundOnSuccess:
		return color.White
	case theme.ColorNameForegroundOnWarning:
		return colBackground
	case theme.ColorNameHeaderBackground:
		return colSurface
	case theme.ColorNameHover:
		return rgba(0xa5, 0x6b, 0xff, 0x22)
	case theme.ColorNameHyperlink:
		return colPrimaryLite
	case theme.ColorNameInputBackground:
		return colSurface2
	case theme.ColorNameInputBorder:
		return colBorder
	case theme.ColorNameMenuBackground:
		return colSurface
	case theme.ColorNameOverlayBackground:
		return colSurface
	case theme.ColorNamePlaceHolder:
		return colTextMuted
	case theme.ColorNamePressed:
		return rgba(0xa5, 0x6b, 0xff, 0x44)
	case theme.ColorNamePrimary:
		return colPrimary
	case theme.ColorNameScrollBar:
		return rgba(0xa5, 0x6b, 0xff, 0x66)
	case theme.ColorNameScrollBarBackground:
		return rgba(0, 0, 0, 0x22)
	case theme.ColorNameSelection:
		return rgba(0xa5, 0x6b, 0xff, 0x55)
	case theme.ColorNameSeparator:
		return colBorder
	case theme.ColorNameShadow:
		return rgba(0, 0, 0, 0x88)
	case theme.ColorNameSuccess:
		return colGood
	case theme.ColorNameWarning:
		return colWarn
	}
	return theme.DefaultTheme().Color(n, theme.VariantDark)
}

func (purpleTheme) Font(s fyne.TextStyle) fyne.Resource { return theme.DefaultTheme().Font(s) }

func (purpleTheme) Icon(n fyne.ThemeIconName) fyne.Resource { return theme.DefaultTheme().Icon(n) }

func (purpleTheme) Size(n fyne.ThemeSizeName) float32 {
	switch n {
	case theme.SizeNameText:
		return 13.5
	case theme.SizeNameHeadingText:
		return 26
	case theme.SizeNameSubHeadingText:
		return 17
	case theme.SizeNameCaptionText:
		return 11.5
	case theme.SizeNameInputRadius, theme.SizeNameButtonRadius:
		return 8
	case theme.SizeNameSelectionRadius:
		return 6
	case theme.SizeNameCardRadius, theme.SizeNameDialogRadius, theme.SizeNamePopupRadius, theme.SizeNameMenuRadius:
		return 12
	case theme.SizeNameScrollBarRadius:
		return 4
	}
	return theme.DefaultTheme().Size(n)
}
