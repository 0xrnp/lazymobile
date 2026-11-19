// The app the device tests install on an emulator. It logs when it starts and when it opens a
// link, and crashes on lazymobilee2e://crash so that crash reports can be read back.
package dev.lazymobile.e2e;

import android.app.Activity;
import android.net.Uri;
import android.os.Bundle;
import android.util.Log;
import android.widget.TextView;

public class Main extends Activity {
    @Override
    protected void onCreate(Bundle state) {
        super.onCreate(state);
        TextView text = new TextView(this);
        text.setText("lazymobile e2e");
        setContentView(text);
        Log.i("LAZYMOBILE_E2E", "launched");
        Uri uri = getIntent().getData();
        if (uri != null) {
            Log.i("LAZYMOBILE_E2E", "opened " + uri);
            if ("crash".equals(uri.getHost())) {
                throw new RuntimeException("LAZYMOBILE_E2E crash on purpose: " + uri);
            }
        }
    }
}
