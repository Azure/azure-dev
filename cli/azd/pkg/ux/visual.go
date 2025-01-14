package ux

type VisualContext struct {
	// The size of the visual
	Size CanvasSize
	// The relative row position of the visual within the canvas
	Top int
}

type Visual interface {
	Render(printer Printer) error
	WithCanvas(canvas Canvas) Visual
}

type visualElement struct {
	canvas   Canvas
	renderFn func(printer Printer) error
}

func NewVisualElement(renderFn RenderFn) *visualElement {
	return &visualElement{
		renderFn: renderFn,
	}
}

func (v *visualElement) WithCanvas(canvas Canvas) Visual {
	v.canvas = canvas
	return v
}

func (v *visualElement) Render(printer Printer) error {
	return v.renderFn(printer)
}
