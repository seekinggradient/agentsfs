import { Extension } from "@tiptap/core";
import { Plugin } from "@tiptap/pm/state";

// Embedded browsers and native Edit menus can send Input Events instead of
// keyboard events. Both must use ProseMirror's history, never the DOM's history.
export const NativeHistory = Extension.create({
  name: "nativeHistory",
  priority: 1100,
  addKeyboardShortcuts() {
    const undo = () => this.editor.commands.undo();
    const redo = () => this.editor.commands.redo();
    return {
      "Ctrl-z": undo,
      "Meta-z": undo,
      "Ctrl-Shift-z": redo,
      "Meta-Shift-z": redo,
      "Ctrl-y": redo,
      "Meta-y": redo,
    };
  },
  addProseMirrorPlugins() {
    return [
      new Plugin({
        props: {
          handleDOMEvents: {
            beforeinput: (_view, event) => {
              if (
                event.inputType !== "historyUndo" &&
                event.inputType !== "historyRedo"
              )
                return false;
              if (!event.cancelable) return false;
              event.preventDefault();
              this.editor.commands[
                event.inputType === "historyUndo" ? "undo" : "redo"
              ]();
              return true;
            },
          },
        },
      }),
    ];
  },
});
