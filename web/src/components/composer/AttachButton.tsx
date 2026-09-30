import { PlusIcon } from "lucide-react";
import { useRef } from "react";

import { Button } from "~/components/ui/button";

/** The file picker: a hidden input, and the button that opens it when the
    composer can take files at all. */
export function AttachButton({
  show,
  disabled,
  onFiles,
}: {
  show: boolean;
  disabled: boolean;
  onFiles: (files: File[]) => void;
}) {
  const fileInputRef = useRef<HTMLInputElement>(null);
  return (
    <>
      <input
        ref={fileInputRef}
        type="file"
        multiple
        className="hidden"
        onChange={(e) => {
          onFiles(Array.from(e.target.files ?? []));
          // Cleared so picking the same file twice in a row still fires.
          e.target.value = "";
        }}
      />
      {show && (
        <Button
          type="button"
          variant="ghost"
          size="icon"
          disabled={disabled}
          onClick={() => fileInputRef.current?.click()}
          aria-label="Attach files"
          title="Attach files"
          className="text-muted-foreground hover:text-foreground size-11 shrink-0 rounded-full md:size-8"
        >
          <PlusIcon />
        </Button>
      )}
    </>
  );
}
